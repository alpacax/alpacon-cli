package kube

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type statusError struct{ code int }

func (e statusError) Error() string       { return "status" }
func (e statusError) HTTPStatusCode() int { return e.code }

func TestCredentialExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	saved := now.Add(time.Hour)
	later := now.Add(3 * time.Hour)
	past := now.Add(-time.Minute)
	read := func(s *kubernetes.KubeSessionDetail, err error) func() (*kubernetes.KubeSessionDetail, error) {
		return func() (*kubernetes.KubeSessionDetail, error) { return s, err }
	}
	tests := []struct {
		name     string
		saved    time.Time
		retrieve func() (*kubernetes.KubeSessionDetail, error)
		want     time.Time
		gone     bool
		refused  bool
		warn     bool
	}{
		{name: "open session", saved: saved, retrieve: read(&kubernetes.KubeSessionDetail{TokenExpiredAt: &saved}, nil), want: saved},
		{name: "extended session", saved: past, retrieve: read(&kubernetes.KubeSessionDetail{TokenExpiredAt: &later}, nil), want: later},
		{name: "closed session", saved: saved, retrieve: read(&kubernetes.KubeSessionDetail{TokenExpiredAt: &saved, ClosedAt: &past}, nil), gone: true},
		{name: "expired on the server", saved: saved, retrieve: read(&kubernetes.KubeSessionDetail{TokenExpiredAt: &past}, nil), gone: true},
		{name: "no expiry in the answer", saved: saved, retrieve: read(&kubernetes.KubeSessionDetail{}, nil), want: saved},
		{name: "session not found", saved: saved, retrieve: read(nil, statusError{404}), gone: true},
		{name: "check forbidden", saved: saved, retrieve: read(nil, statusError{403}), gone: true, refused: true},
		{name: "login rejected", saved: saved, retrieve: read(nil, statusError{401}), gone: true, refused: true},
		{name: "server error while the token is good", saved: saved, retrieve: read(nil, statusError{503}), want: saved, warn: true},
		{name: "check fails while the token is good", saved: saved, retrieve: read(nil, errors.New("connection refused")), want: saved, warn: true},
		{name: "check fails after the token expired", saved: past, retrieve: read(nil, errors.New("connection refused")), gone: true},
		{name: "another workspace, token good", saved: saved, want: saved},
		{name: "another workspace, token expired", saved: past, gone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := credentialExpiry(&kubeSessionRecord{TokenExpiredAt: tt.saved}, now, tt.retrieve)
			if tt.gone {
				require.ErrorIs(t, err, errSessionGone)
				var refused *refusedCheck
				assert.Equal(t, tt.refused, errors.As(err, &refused))
				return
			}
			if tt.warn {
				require.Error(t, err)
				require.NotErrorIs(t, err, errSessionGone)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// A new kube login fails on the same refusal, so only an ended session is
// sent straight to it.
func TestSessionGoneMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "ended", err: errSessionGone, want: "The kube session for prod-east has ended; run 'alpacon kube login prod-east'."},
		{name: "login rejected", err: &refusedCheck{err: statusError{401}},
			want: "The server no longer accepts this login, so the kube session for prod-east cannot be checked; run 'alpacon login', then run 'alpacon kube login prod-east'."},
		{name: "check forbidden", err: &refusedCheck{err: statusError{403}},
			want: "The server refused to check the kube session for prod-east, so the saved token is not used: status. Resolve that refusal, then run 'alpacon kube login prod-east'."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg, args := sessionGoneMessage("prod-east", tt.err)
			assert.Equal(t, tt.want, fmt.Sprintf(msg, args...))
		})
	}
}

func TestPickRecord(t *testing.T) {
	t.Parallel()
	here := kubeSessionRecord{Cluster: "prod-east", WorkspaceURL: "https://ws.example.com", ProxyURL: "https://ws.example.com/kube/c-1"}
	there := kubeSessionRecord{Cluster: "prod-east", WorkspaceURL: "https://other.example.com", ProxyURL: "https://other.example.com/kube/c-9"}
	both := []kubeSessionRecord{here, there}
	tests := []struct {
		name      string
		records   []kubeSessionRecord
		server    string
		workspace string
		want      string
	}{
		{name: "the server kubectl calls", records: both, server: there.ProxyURL, workspace: here.WorkspaceURL, want: there.WorkspaceURL},
		{name: "an unknown server", records: both, server: "https://elsewhere.example.com/kube/c-1"},
		{name: "the logged-in workspace", records: both, workspace: here.WorkspaceURL, want: here.WorkspaceURL},
		{name: "a lone record", records: []kubeSessionRecord{there}, workspace: here.WorkspaceURL, want: there.WorkspaceURL},
		{name: "two records and no way to choose", records: both, workspace: "https://third.example.com"},
		{name: "none", records: nil, workspace: here.WorkspaceURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pickRecord(tt.records, tt.server, tt.workspace)
			if tt.want == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.want, got.WorkspaceURL)
		})
	}
}

func TestClusterServer(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://ws.example.com/kube/c-1", clusterServer(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"cluster":{"server":"https://ws.example.com/kube/c-1"},"interactive":false}}`))
	assert.Empty(t, clusterServer(`{"spec":{"interactive":false}}`))
	assert.Empty(t, clusterServer(""))
	assert.Empty(t, clusterServer("not json"))
}

func TestRetrieveWithinGivesUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		block := make(chan struct{})
		start := time.Now()

		_, err := retrieveWithin(retrieveTimeout, func() (*kubernetes.KubeSessionDetail, error) {
			<-block
			return nil, nil
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no answer within")
		assert.Equal(t, retrieveTimeout, time.Since(start))
		close(block)
	})
}

func TestStoreRecordsFindsEveryWorkspace(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: t.TempDir()}
	here := testRecord("prod-east")
	there := testRecord("prod-east")
	there.WorkspaceURL = "https://other.example.com"
	require.NoError(t, store.write(here))
	require.NoError(t, store.write(there))
	require.NoError(t, store.write(testRecord("prod-west")))

	got, err := store.records("prod-east")
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// loggedIn writes a login under a fresh HOME and returns it.
func loggedIn(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "access-1", "refresh-1", "", 3600, false))
	login, err := config.LoadConfig()
	require.NoError(t, err)
	return login
}

// Serial: it sets HOME.
func TestSaveExtendedRecordKeepsANewerLogin(t *testing.T) {
	login := loggedIn(t)
	store := kubeSessionStore{dir: t.TempDir()}
	newer := testRecord("prod-east")
	newer.KubeSessionID = "k-2"
	require.NoError(t, store.write(newer))

	stale := testRecord("prod-east")
	stale.TokenExpiredAt = stale.TokenExpiredAt.Add(time.Hour)
	saveExtendedRecord(store, login, stale)

	got, err := store.read(testKey("prod-east"))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "k-2", got.KubeSessionID, "a record from an older session never replaces a newer login's")
}

// Serial: it sets HOME. A moved expiry is not saved for a login a logout
// ended, or the record the logout removed would come back.
func TestSaveExtendedRecordSkipsAfterALogout(t *testing.T) {
	login := loggedIn(t)
	store := kubeSessionStore{dir: t.TempDir()}
	record := testRecord("prod-east")
	require.NoError(t, store.write(record))
	require.NoError(t, config.DeleteConfig(login))

	moved := record
	moved.TokenExpiredAt = record.TokenExpiredAt.Add(time.Hour)
	saveExtendedRecord(store, login, moved)

	got, err := store.read(record.key())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, record.TokenExpiredAt, got.TokenExpiredAt)
}

// Serial: it sets HOME. A logout during a kube login's approval wait leaves
// the lock that login holds, so a second login to the cluster is still kept
// out instead of locking a new file at the same path.
func TestLogoutKeepsTheLockAWaitingLoginHolds(t *testing.T) {
	login := loggedIn(t)
	store, err := defaultKubeSessionStore()
	require.NoError(t, err)
	held, err := store.lock(testKey("prod-east"))
	require.NoError(t, err)
	defer func() { _ = held.Release() }()

	require.NoError(t, config.DeleteConfig(login))

	_, err = store.lock(testKey("prod-east"))
	require.ErrorIs(t, err, errStoreLocked)
}

// Serial: it sets HOME. A logout removes every file the store writes for a
// record, the staging file a write died before renaming included, so a change
// to the store's naming that logout no longer matches fails here.
func TestLogoutRemovesWhatTheStoreWrites(t *testing.T) {
	login := loggedIn(t)
	store, err := defaultKubeSessionStore()
	require.NoError(t, err)
	record := testRecord("prod-east")
	require.NoError(t, store.write(record))
	staging, err := os.CreateTemp(store.dir, stagingPattern(record.key()))
	require.NoError(t, err)
	require.NoError(t, staging.Close())

	require.NoError(t, config.DeleteConfig(login))

	assert.NoFileExists(t, store.recordPath(record.key()))
	assert.NoFileExists(t, staging.Name())
}
