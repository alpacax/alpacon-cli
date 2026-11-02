package localsignal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		{"out of order timestamp", "ses-1", Denied, -time.Second, false, 0},
		{"fold one", "ses-1", Denied, time.Second, false, 0},
		{"fold two", "ses-1", Denied, 59 * time.Second, false, 0},
		{"separate kind", "ses-1", Paused, time.Second, true, 0},
		{"separate session", "ses-2", Denied, time.Second, true, 0},
		{"no session", "", Denied, time.Second, true, 0},
		{"no session fold", "", Denied, 2 * time.Second, false, 0},
		{"boundary", "ses-1", Denied, time.Minute, true, 3},
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
	return allow(statePath, session, kind, func() time.Time { return now })
}
