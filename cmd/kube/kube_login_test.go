package kube

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/api/types"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseKubeScopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     []string
		want    []string
		wantErr string
	}{
		{name: "kube", raw: []string{"kube"}, want: []string{"kube"}},
		{name: "both", raw: []string{"kube_exec", "kube"}, want: []string{"kube", "kube_exec"}},
		{name: "kube_exec implies kube", raw: []string{"kube_exec"}, want: []string{"kube", "kube_exec"}},
		{name: "repeated", raw: []string{"kube", " kube "}, want: []string{"kube"}},
		{name: "typo", raw: []string{"kube", "kube-exec"}, wantErr: "holds only kube and kube_exec, not kube-exec"},
		{name: "server scope", raw: []string{"command"}, wantErr: "not command"},
		{name: "blank", raw: []string{" "}, wantErr: "needs the kube scope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseKubeScopes(tt.raw)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReadKubeLoginInput(t *testing.T) {
	t.Parallel()
	agent := aiagent.Detection{Name: "claude-code", Markers: []string{"CLAUDECODE"}}
	tests := []struct {
		name     string
		cluster  string
		flags    kubeLoginFlagValues
		set      []string
		env      string
		detected aiagent.Detection
		check    func(t *testing.T, in kubeLoginInput)
		wantErr  string
	}{
		{
			name: "defaults",
			check: func(t *testing.T, in kubeLoginInput) {
				t.Helper()
				assert.Equal(t, "user", in.requesterType)
				assert.Equal(t, 5*time.Minute, in.waitTimeout)
				assert.Nil(t, in.scopes)
				assert.Empty(t, in.workSession)
			},
		},
		{
			name:  "new session flags",
			flags: kubeLoginFlagValues{intent: " debug ", scopes: []string{"kube_exec"}, duration: "2h", waitApproval: "30m", proxyPort: 18443},
			set:   []string{"intent", "scope", "duration", "wait-approval", "proxy-port"},
			check: func(t *testing.T, in kubeLoginInput) {
				t.Helper()
				assert.Equal(t, "debug", in.intent)
				assert.Equal(t, []string{"kube", "kube_exec"}, in.scopes)
				assert.Equal(t, 2*time.Hour, in.duration)
				assert.Equal(t, 30*time.Minute, in.waitTimeout)
				assert.Equal(t, 18443, in.proxyPort)
			},
		},
		{
			name:  "flag outranks the environment",
			flags: kubeLoginFlagValues{workSession: "ws-flag"},
			set:   []string{"work-session"},
			env:   "ws-env",
			check: func(t *testing.T, in kubeLoginInput) {
				t.Helper()
				assert.Equal(t, "ws-flag", in.workSession)
				assert.Empty(t, in.envSession)
			},
		},
		{
			name: "environment offers a session",
			env:  " ws-env ",
			check: func(t *testing.T, in kubeLoginInput) {
				t.Helper()
				assert.Empty(t, in.workSession, "a session from the environment is not used as given")
				assert.Equal(t, "ws-env", in.envSession)
			},
		},
		{
			name:  "environment with new session flags",
			flags: kubeLoginFlagValues{duration: "1h"},
			set:   []string{"duration"},
			env:   "ws-env",
			check: func(t *testing.T, in kubeLoginInput) { t.Helper(); assert.Equal(t, time.Hour, in.duration) },
		},
		{
			name:     "agent gets an agent session",
			detected: agent,
			check:    func(t *testing.T, in kubeLoginInput) { t.Helper(); assert.Equal(t, "agent", in.requesterType) },
		},
		{name: "agent with kube_exec", flags: kubeLoginFlagValues{scopes: []string{"kube", "kube_exec"}}, set: []string{"scope"}, detected: agent, wantErr: `"kube_exec" is not allowed for agent sessions`},
		{name: "blank cluster", cluster: "  ", wantErr: "CLUSTER must name a cluster"},
		{name: "empty work session", set: []string{"work-session"}, wantErr: "--work-session must name"},
		{name: "bad wait", flags: kubeLoginFlagValues{waitApproval: "soon"}, set: []string{"wait-approval"}, wantErr: "invalid --wait-approval value"},
		{name: "empty intent", set: []string{"intent"}, wantErr: "--intent cannot be empty"},
		{name: "bad duration", flags: kubeLoginFlagValues{duration: "-1h"}, set: []string{"duration"}, wantErr: "must be a positive duration"},
		{name: "bad port", flags: kubeLoginFlagValues{proxyPort: 0}, set: []string{"proxy-port"}, wantErr: "--proxy-port must be a port number"},
		{name: "session and scope", flags: kubeLoginFlagValues{workSession: "ws-1", scopes: []string{"kube"}}, set: []string{"work-session", "scope"}, wantErr: "cannot be combined with --work-session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			changed := func(name string) bool { return slices.Contains(tt.set, name) }
			cluster := tt.cluster
			if cluster == "" {
				cluster = "prod-east"
			}
			in, err := readKubeLoginInput(cluster, tt.flags, changed, tt.env, tt.detected)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "prod-east", in.cluster)
			tt.check(t, in)
		})
	}
}

func clusterSession(id, status string, mutate func(*wsapi.WorkSession)) wsapi.WorkSession {
	ws := wsapi.WorkSession{
		ID:            id,
		Status:        status,
		RequesterType: "user",
		Scopes:        []string{"kube"},
		Clusters:      []types.ClusterSummary{{ID: "c-1", Name: "prod-east"}},
		ExpiresAt:     time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC),
	}
	if mutate != nil {
		mutate(&ws)
	}
	return ws
}

func TestSessionWantUsable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	want := sessionWant{clusterID: "c-1", requesterType: "user"}
	tests := []struct {
		name string
		ws   wsapi.WorkSession
		want sessionWant
		ok   bool
	}{
		{name: "active cluster session", ws: clusterSession("a", "active", nil), want: want, ok: true},
		{name: "cluster ID compared without case", ws: clusterSession("a", "active", func(ws *wsapi.WorkSession) { ws.Clusters[0].ID = "C-1" }), want: want, ok: true},
		{name: "server session", ws: clusterSession("a", "active", func(ws *wsapi.WorkSession) {
			ws.Clusters = nil
			ws.Scopes = []string{"command"}
			ws.Servers = []types.ServerSummary{{ID: "s-1", Name: "web-01"}}
		}), want: want},
		{name: "another cluster", ws: clusterSession("a", "active", func(ws *wsapi.WorkSession) { ws.Clusters[0].ID = "c-2" }), want: want},
		{name: "pending", ws: clusterSession("a", "pending", nil), want: want},
		{name: "expired by time", ws: clusterSession("a", "active", func(ws *wsapi.WorkSession) { ws.ExpiresAt = now.Add(-time.Minute) }), want: want},
		{name: "starts later", ws: clusterSession("a", "active", func(ws *wsapi.WorkSession) { ws.StartsAt = &future }), want: want},
		{name: "agent session for a person", ws: clusterSession("a", "active", func(ws *wsapi.WorkSession) { ws.RequesterType = "agent" }), want: want},
		{name: "missing a wanted scope", ws: clusterSession("a", "active", nil), want: sessionWant{clusterID: "c-1", requesterType: "user", scopes: []string{"kube", "kube_exec"}}},
		{name: "holding the wanted scopes", ws: clusterSession("a", "active", func(ws *wsapi.WorkSession) { ws.Scopes = []string{"kube", "kube_exec"} }), want: sessionWant{clusterID: "c-1", requesterType: "user", scopes: []string{"kube", "kube_exec"}}, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.ok, tt.want.usable(&tt.ws, now))
		})
	}
}

func TestPickClusterSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	want := sessionWant{clusterID: "c-1", requesterType: "user"}
	older := clusterSession("old", "pending", func(ws *wsapi.WorkSession) { ws.AddedAt = now.Add(-2 * time.Hour) })
	newer := clusterSession("new", "approved", func(ws *wsapi.WorkSession) { ws.AddedAt = now.Add(-time.Hour) })
	active := clusterSession("act", "active", nil)
	ended := clusterSession("end", "rejected", nil)
	durationBased := clusterSession("dur", "pending", func(ws *wsapi.WorkSession) { ws.ExpiresAt = time.Time{} })

	tests := []struct {
		name     string
		sessions []wsapi.WorkSession
		wantID   string
		wantWait bool
	}{
		{name: "none", sessions: nil},
		{name: "only ended", sessions: []wsapi.WorkSession{ended}},
		{name: "usable wins over waiting", sessions: []wsapi.WorkSession{older, active}, wantID: "act"},
		{name: "newest waiting", sessions: []wsapi.WorkSession{older, newer}, wantID: "new", wantWait: true},
		{name: "no expiry before activation", sessions: []wsapi.WorkSession{durationBased}, wantID: "dur", wantWait: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws, wait := pickClusterSession(tt.sessions, want, now)
			if tt.wantID == "" {
				assert.Nil(t, ws)
				assert.False(t, wait)
				return
			}
			require.NotNil(t, ws)
			assert.Equal(t, tt.wantID, ws.ID)
			assert.Equal(t, tt.wantWait, wait)
		})
	}
}

func TestFormatExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.Local)
	assert.Equal(t, "18:40", formatExpiry(time.Date(2026, 10, 21, 18, 40, 0, 0, time.Local), now))
	assert.Equal(t, "2026-10-22 01:10", formatExpiry(time.Date(2026, 10, 22, 1, 10, 0, 0, time.Local), now))
}

func TestCredentialPluginNamesTheCluster(t *testing.T) {
	t.Parallel()
	exec := credentialPlugin("prod-east")
	assert.Equal(t, "alpacon", exec.Command)
	assert.Equal(t, []string{"kube", "credentials", "--cluster", "prod-east"}, exec.Args)
}

func testRecord(cluster string) kubeSessionRecord {
	return kubeSessionRecord{
		Cluster:        cluster,
		ClusterID:      "c-1",
		WorkspaceName:  "ws",
		WorkspaceURL:   "https://ws.example.com",
		KubeSessionID:  "k-1",
		WorkSessionID:  "w-1",
		ChannelToken:   "tok",
		TokenExpiredAt: time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC),
		ProxyURL:       "https://ws.example.com/kube/c-1",
		SavedAt:        time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
	}
}

func TestKubeSessionStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: filepath.Join(t.TempDir(), ".alpacon", "kube")}

	got, err := store.read("prod-east")
	require.NoError(t, err)
	assert.Nil(t, got, "no record yet")

	require.NoError(t, store.write(testRecord("prod-east")))
	got, err = store.read("prod-east")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, testRecord("prod-east"), *got)
	assert.True(t, got.sameWorkspace("ws", "https://ws.example.com"), "record workspace %q at %q", got.WorkspaceName, got.WorkspaceURL)
	assert.False(t, got.sameWorkspace("ws", "https://other.example.com"), "record workspace %q at %q", got.WorkspaceName, got.WorkspaceURL)

	other, err := store.read("prod-west")
	require.NoError(t, err)
	assert.Nil(t, other, "each cluster has its own record")
}

func TestKubeSessionStoreRestore(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: t.TempDir()}
	previous := testRecord("prod-east")
	require.NoError(t, store.write(previous))
	replaced := testRecord("prod-east")
	replaced.KubeSessionID = "k-2"
	require.NoError(t, store.write(replaced))

	require.NoError(t, store.restore("prod-east", &previous))
	got, err := store.read("prod-east")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "k-1", got.KubeSessionID)

	require.NoError(t, store.restore("prod-east", nil))
	got, err = store.read("prod-east")
	require.NoError(t, err)
	assert.Nil(t, got, "with no previous record the new one is removed")
	require.NoError(t, store.restore("prod-east", nil), "removing a missing record is not an error")
}

// A record stored under the file another name hashes to is not taken for
// that name's record.
func TestKubeSessionStoreIgnoresARecordForAnotherName(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: t.TempDir()}
	require.NoError(t, store.write(testRecord("prod-east")))
	require.NoError(t, os.Rename(store.recordPath("prod-east"), store.recordPath("prod-west")))

	got, err := store.read("prod-west")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestKubeSessionStoreLockIsExclusive(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: t.TempDir()}
	lock, err := store.lock("prod-east")
	require.NoError(t, err)
	_, err = store.lock("prod-east")
	require.ErrorIs(t, err, errStoreLocked)
	other, err := store.lock("prod-west")
	require.NoError(t, err, "another cluster has its own lock")
	require.NoError(t, other.Release())
	require.NoError(t, lock.Release())
	again, err := store.lock("prod-east")
	require.NoError(t, err)
	require.NoError(t, again.Release())
}

func TestKubeSessionStoreIsOwnerOnly(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix modes")
	}
	dir := filepath.Join(t.TempDir(), "kube")
	require.NoError(t, os.MkdirAll(dir, 0755))
	store := kubeSessionStore{dir: dir}
	path := store.recordPath("prod-east")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0644))

	require.NoError(t, store.write(testRecord("prod-east")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "a wider file left before is replaced owner-only")
	info, err = os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

func TestKubeSessionStoreReplacesASymlinkRatherThanWritingThroughIt(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs privileges on Windows")
	}
	store := kubeSessionStore{dir: t.TempDir()}
	target := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(target, []byte("keep"), 0644))
	require.NoError(t, os.Symlink(target, store.recordPath("prod-east")))

	require.NoError(t, store.write(testRecord("prod-east")))

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
	info, err := os.Lstat(store.recordPath("prod-east"))
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), "the record is a plain file now: %v", info.Mode())
}

// A kubeconfig that cannot be written leaves the store as it was before the
// login and closes the session it opened.
func TestOpenKubeSessionRestoresTheRecordWhenTheWriteFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("a read-only directory does not stop a write on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	srv := newSessionServer()
	ac := srv.start(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	editor := openEditor(t, path)
	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	store := kubeSessionStore{dir: t.TempDir()}
	previous := testRecord("prod-east")
	require.NoError(t, store.write(previous))
	writer := kubeSessionWriter{
		exec: credentialPlugin("prod-east"),
		persist: func(session *kubernetes.KubeSessionResponse) (func() error, error) {
			record := testRecord("prod-east")
			record.KubeSessionID = session.ID
			if err := store.write(record); err != nil {
				return nil, err
			}
			return func() error { return store.restore("prod-east", &previous) }, nil
		},
	}

	_, err := openKubeSession(ac, editor, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path}, "c-prod", writer)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write "+path)
	assert.Equal(t, 1, srv.closes())
	got, err := store.read("prod-east")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, previous, *got)
}

// A session the store could not keep is closed before any context points at it.
func TestOpenKubeSessionClosesTheSessionWhenItCannotBeKept(t *testing.T) {
	t.Parallel()
	srv := newSessionServer()
	ac := srv.start(t)
	path := filepath.Join(t.TempDir(), "config")
	editor := openEditor(t, path)
	writer := kubeSessionWriter{
		exec: credentialPlugin("prod-east"),
		persist: func(*kubernetes.KubeSessionResponse) (func() error, error) {
			return nil, errors.New("disk full")
		},
	}

	_, err := openKubeSession(ac, editor, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path}, "c-prod", writer)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
	assert.Equal(t, 1, srv.closes())
	assert.NoFileExists(t, path)
}

// Another workspace's session cannot be closed with this login's credentials,
// so it is left to end with its work session.
func TestClosePreviousSessionSkipsAnotherWorkspace(t *testing.T) {
	srv := newSessionServer()
	ac := srv.start(t)
	ac.WorkspaceName = "ws"
	record := testRecord("prod-east")
	record.WorkspaceName = "other"

	_, stderr := testutil.CaptureOutput(t, func() {
		closePreviousSession(ac, &record, "k-new")
	})

	assert.Zero(t, srv.closes())
	assert.Contains(t, stderr, "belongs to workspace other")

	record.WorkspaceName, record.WorkspaceURL = "ws", ac.BaseURL
	closePreviousSession(ac, &record, record.KubeSessionID)
	assert.Zero(t, srv.closes(), "the session this login opened is not the previous one")
}
