package localsignal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimiterKeysAndFolding(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state", "signals.json")
	now := time.Now()
	for _, tc := range []struct {
		name, session string
		kind          Kind
		after         time.Duration
		wantEmit      bool
		wantFolded    int
	}{
		{"first", "ses-1", Denied, 0, true, 0},
		{"fold one", "ses-1", Denied, time.Second, false, 0},
		{"fold two", "ses-1", Denied, 59 * time.Second, false, 0},
		{"separate kind", "ses-1", Paused, time.Second, true, 0},
		{"separate session", "ses-2", Denied, time.Second, true, 0},
		{"no session", "", Denied, time.Second, true, 0},
		{"no session fold", "", Denied, 2 * time.Second, false, 0},
		{"boundary", "ses-1", Denied, time.Minute, true, 2},
		{"reset count", "ses-1", Denied, 2 * time.Minute, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			folded, emit, err := allowAt(path, tc.session, tc.kind, now.Add(tc.after))
			require.NoError(t, err)
			assert.Equal(t, tc.wantEmit, emit)
			assert.Equal(t, tc.wantFolded, folded)
		})
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var state map[string]entry
	require.NoError(t, json.Unmarshal(data, &state))
	assert.Len(t, state, 4)
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".signals-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func TestLimiterFailsOpen(t *testing.T) {
	t.Parallel()
	for _, contents := range []string{"corrupt", "null", "unreadable"} {
		t.Run(contents, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "signals.json")
			if contents == "unreadable" {
				require.NoError(t, os.Mkdir(path, 0700))
			} else {
				require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
			}
			_, emit, err := allowAt(path, "ses-1", Denied, time.Now())
			assert.True(t, emit, "bad state must emit for %s", contents)
			if contents == "unreadable" {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			var state map[string]entry
			require.NoError(t, json.Unmarshal(data, &state))
			assert.Contains(t, state, "denied:ses-1")
		})
	}
}

func TestLimiterAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signals.json")
	now := time.Now().Unix()
	var children []*exec.Cmd
	var outputs []*bytes.Buffer
	for range 6 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLimiterHelper$", "--", path, strconv.FormatInt(now, 10))
		cmd.Env = append(os.Environ(), "GO_WANT_SIGNAL_HELPER=1")
		output := &bytes.Buffer{}
		cmd.Stdout = output
		children = append(children, cmd)
		outputs = append(outputs, output)
		require.NoError(t, cmd.Start())
	}
	for _, cmd := range children {
		require.NoError(t, cmd.Wait())
	}
	emitted := 0
	for _, output := range outputs {
		if output.String() == "true 0\n" {
			emitted++
		} else {
			assert.Equal(t, "false 0\n", output.String())
		}
	}
	assert.Equal(t, 1, emitted)
	folded, emit, err := allowAt(path, "ses-1", Paused, time.Unix(now, 0).Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, emit, "next window must emit")
	assert.Equal(t, 5, folded)
}

func TestLimiterHelper(t *testing.T) {
	if os.Getenv("GO_WANT_SIGNAL_HELPER") != "1" {
		return
	}
	now, err := strconv.ParseInt(os.Args[len(os.Args)-1], 10, 64)
	if err != nil {
		os.Exit(2)
	}
	folded, emit, err := allowAt(os.Args[len(os.Args)-2], "ses-1", Paused, time.Unix(now, 0))
	if err != nil {
		os.Exit(2)
	}
	fmt.Printf("%t %d\n", emit, folded)
	os.Exit(0)
}

func TestLimiterContentionPreservesFirstEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signals.json")
	lock, err := filelock.Acquire(path+".lock", 0600)
	require.NoError(t, err)
	synctest.Test(t, func(t *testing.T) {
		released := make(chan error, 1)
		go func() { time.Sleep(250 * time.Millisecond); released <- lock.Release() }()
		_, emit, err := allowAt(path, "ses-1", Paused, time.Now())
		require.NoError(t, err)
		require.NoError(t, <-released)
		assert.True(t, emit, "waiting for a writer must preserve the first event")
		_, emit, err = allowAt(path, "ses-1", Paused, time.Now())
		require.NoError(t, err)
		assert.False(t, emit, "another event in the window must fold")
	})
}

func allowAt(statePath, session string, kind Kind, now time.Time) (int, bool, error) {
	return allow(statePath, "", session, kind, func() time.Time { return now })
}

func TestLimiterFuturePruningAndKeyBound(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "signals.json")
	now := time.Now()
	state := map[string]entry{
		"denied:ses-1":  {Last: now.Add(time.Hour), Folded: 9},
		"denied:old":    {Last: now.Add(-25 * time.Hour)},
		"paused:folded": {Last: now.Add(-25 * time.Hour), Folded: 2},
	}
	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	folded, emit, err := allowAt(path, "ses-1", Denied, now)
	require.NoError(t, err)
	assert.True(t, emit, "a future timestamp must expire")
	assert.Zero(t, folded)
	_, _, err = allowAt(path, strings.Repeat("x", 200), Paused, now)
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	// Read into a fresh map so removed entries do not survive from the fixture.
	state = nil
	require.NoError(t, json.Unmarshal(data, &state))
	assert.NotContains(t, state, "denied:old")
	assert.NotContains(t, state, "paused:folded")
	assert.Contains(t, state, "paused:"+strings.Repeat("x", 128))
	folded, emit, err = allowAt(path, "folded", Paused, now)
	require.NoError(t, err)
	assert.True(t, emit, "an event after pruning must start fresh")
	assert.Zero(t, folded)
}

func TestLimiterRecentStoredTimestampFolds(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "signals.json")
	now := time.Now()
	data, err := json.Marshal(map[string]entry{
		"denied:ses-1": {Last: now.Add(-10 * time.Second), Folded: 2},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	folded, emit, err := allowAt(path, "ses-1", Denied, now)
	require.NoError(t, err)
	assert.False(t, emit, "a recent stored timestamp must retain the rate limit")
	assert.Zero(t, folded)
	folded, emit, err = allowAt(path, "ses-1", Denied, now.Add(50*time.Second))
	require.NoError(t, err)
	assert.True(t, emit, "the original window must expire after sixty seconds")
	assert.Equal(t, 3, folded)
}

func TestLimiterLockTimeoutFailsOpenWithoutWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signals.json")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0600))
	lock, err := filelock.Acquire(path+".lock", 0600)
	require.NoError(t, err)
	defer func() { _ = lock.Release() }()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		_, emit, err := allowAt(path, "ses-1", Paused, time.Now())
		require.ErrorIs(t, err, filelock.ErrLocked)
		assert.True(t, emit, "lock timeout must fail open")
		assert.Equal(t, 500*time.Millisecond, time.Since(start))
	})
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "original", string(data))
}

func TestOversizedStateFailsOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "signals.json")
	now := time.Now()
	data, err := json.Marshal(map[string]entry{"denied:ses-1": {Last: now}})
	require.NoError(t, err)
	data = append(data, bytes.Repeat([]byte(" "), 64*1024)...)
	require.NoError(t, os.WriteFile(path, data, 0600))
	_, emit, err := allowAt(path, "ses-1", Denied, now)
	require.NoError(t, err)
	assert.True(t, emit, "oversized state must fail open")
}

func TestLimiterWholeStepDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signals.json")
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		finished := make(chan struct{})
		start := time.Now()
		_, emit, err := allow(path, "", "ses-1", Denied, func() time.Time { defer close(finished); <-release; return time.Now() })
		require.Error(t, err)
		assert.True(t, emit, "the entire limiter must be bounded")
		assert.Equal(t, 500*time.Millisecond, time.Since(start))
		close(release)
		<-finished
		synctest.Wait()
	})
	_, err := os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
