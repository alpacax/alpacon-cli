package kube

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/kubeconfig"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	logoutWorkspace = "https://ws.example.com"
	prodServer      = "https://ws.example.com/kube/c-1"
)

// logoutFixture is a store holding one record for prod-east and a closer that
// answers closeErr and counts its calls.
type logoutFixture struct {
	in     kubeLogoutInput
	record kubeSessionRecord
	closed []string
}

// pluginEntries renders the cluster, context and user kube login writes.
func pluginEntries(cluster, server string) (clusters, contexts, users string) {
	name := contextPrefix + cluster
	clusters = fmt.Sprintf("- cluster:\n    server: %s\n  name: %s\n", server, name)
	contexts = fmt.Sprintf("- context:\n    cluster: %s\n    user: %s\n  name: %s\n", name, name, name)
	users = fmt.Sprintf("- name: %s\n  user:\n    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      args:\n      - kube\n      - credentials\n      - --cluster\n      - %s\n      command: alpacon\n      interactiveMode: Never\n", name, cluster)
	return clusters, contexts, users
}

// kubeconfigWith writes a kubeconfig holding the plugin entries of each
// cluster (name=server) and a context of someone else's.
func kubeconfigWith(t *testing.T, current string, clusters ...[2]string) string {
	t.Helper()
	var cl, co, us strings.Builder
	cl.WriteString("- cluster:\n    server: https://other.example.com\n  name: other\n")
	co.WriteString("- context:\n    cluster: other\n    user: other\n  name: other\n")
	us.WriteString("- name: other\n  user:\n    token: theirs\n")
	for _, c := range clusters {
		a, b, d := pluginEntries(c[0], c[1])
		cl.WriteString(a)
		co.WriteString(b)
		us.WriteString(d)
	}
	path := filepath.Join(t.TempDir(), "config")
	body := fmt.Sprintf("apiVersion: v1\nclusters:\n%scontexts:\n%scurrent-context: %s\nkind: Config\nusers:\n%s", cl.String(), co.String(), current, us.String())
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))
	return path
}

func holds(t *testing.T, path, name string) bool {
	t.Helper()
	h, err := kubeconfig.Inspect(path, name)
	require.NoError(t, err)
	return h.Entry
}

func assertHolds(t *testing.T, path, name string) {
	t.Helper()
	assert.True(t, holds(t, path, name), "%s must still hold %s", path, name)
}

func assertGone(t *testing.T, path, name string) {
	t.Helper()
	assert.False(t, holds(t, path, name), "%s must no longer hold %s", path, name)
}

func warningsOf(result *kubeLogoutResult) string {
	var lines []string
	for _, w := range result.warnings {
		lines = append(lines, fmt.Sprintf(w.format, w.args...))
	}
	return strings.Join(lines, "\n")
}

func newLogoutFixture(t *testing.T, closeErr error, paths ...string) *logoutFixture {
	t.Helper()
	f := &logoutFixture{record: testRecord("prod-east")}
	store := kubeSessionStore{dir: t.TempDir()}
	require.NoError(t, store.write(f.record))
	f.in = kubeLogoutInput{
		cluster:      "prod-east",
		paths:        paths,
		store:        store,
		workspaceURL: logoutWorkspace,
		closer: func(r kubeSessionRecord, _ <-chan struct{}) error {
			f.closed = append(f.closed, r.KubeSessionID)
			return closeErr
		},
	}
	return f
}

func TestKubeLogoutRemovesOnlyTheNamedCluster(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer}, [2]string{"dev", "https://ws.example.com/kube/c-2"})
	f := newLogoutFixture(t, nil, path)
	devRecord := testRecord("dev")
	require.NoError(t, f.in.store.write(devRecord))

	result, err := kubeLogout(f.in)
	require.NoError(t, err)

	assert.True(t, result.Found, "%+v", result)
	assert.Equal(t, []string{path}, result.Kubeconfigs)
	assert.True(t, result.CurrentContextCleared, "%+v", result)
	assert.Empty(t, result.EffectiveContext)
	assert.Equal(t, []string{"k-1"}, f.closed)
	assert.Equal(t, []loggedOutSession{{ID: "k-1", Workspace: logoutWorkspace, Closed: true}}, result.Sessions)
	assert.Empty(t, result.warnings)

	assertGone(t, path, "alpacon-prod-east")
	assertHolds(t, path, "alpacon-dev")
	assertHolds(t, path, "other")
	assert.NoFileExists(t, f.in.store.recordPath(f.record.key()))
	assert.FileExists(t, f.in.store.recordPath(devRecord.key()))
}

func TestKubeLogoutKeepsACurrentContextItDoesNotRemove(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	f := newLogoutFixture(t, nil, path)

	result, err := kubeLogout(f.in)
	require.NoError(t, err)

	assert.False(t, result.CurrentContextCleared, "%+v", result)
	assert.Equal(t, "other", result.EffectiveContext, "kubectl still uses it, and the result says so")
	current, err := kubeconfig.CurrentContext([]string{path})
	require.NoError(t, err)
	assert.Equal(t, "other", current)
}

// The session goes last: whatever the server answers, nothing local is left.
func TestKubeLogoutCloseOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		closeErr    error
		wantClosed  bool
		notFound    bool
		wantWarning string
	}{
		{name: "closed", wantClosed: true},
		{name: "not found", closeErr: statusError{404}, notFound: true, wantWarning: "has no kube session k-1 that this login can see"},
		{name: "server error", closeErr: statusError{503}, wantWarning: "stays open until its WorkSession ends"},
		{name: "no answer", closeErr: errNoAnswer, wantWarning: "stays open until its WorkSession ends"},
		{name: "refused", closeErr: statusError{403}, wantWarning: "Failed to close the kube session k-1"},
		{name: "interrupted", closeErr: errCallInterrupted, wantWarning: "Interrupted before closing the kube session k-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
			f := newLogoutFixture(t, tt.closeErr, path)
			// The local half must be done by the time the server is asked.
			inner := f.in.closer
			f.in.closer = func(r kubeSessionRecord, cancel <-chan struct{}) error {
				assertGone(t, path, "alpacon-prod-east")
				assert.NoFileExists(t, f.in.store.recordPath(r.key()), "the token goes before the close")
				lock, err := f.in.store.lock(r.key())
				if assert.NoError(t, err, "no lock is held across the close") {
					_ = lock.Release()
				}
				assert.NoFileExists(t, path+".lock", "no lock is held across the close")
				return inner(r, cancel)
			}

			result, err := kubeLogout(f.in)
			require.NoError(t, err, "a failed close is only a warning")

			require.Len(t, result.Sessions, 1)
			assert.Equal(t, tt.wantClosed, result.Sessions[0].Closed)
			assert.Equal(t, tt.notFound, result.Sessions[0].NotFound)
			if tt.wantWarning == "" {
				assert.Empty(t, result.warnings)
				assert.Empty(t, result.Sessions[0].CloseError)
				return
			}
			assert.Contains(t, warningsOf(result), tt.wantWarning)
			assert.Equal(t, !tt.notFound, result.Sessions[0].CloseError != "", "%+v", result.Sessions[0])
		})
	}
}

func TestKubeLogoutWithoutTheSessionsWorkspaceSkipsTheClose(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		workspaceURL string
	}{
		{name: "logged out", workspaceURL: ""},
		{name: "another workspace", workspaceURL: "https://elsewhere.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
			f := newLogoutFixture(t, nil, path)
			f.in.workspaceURL = tt.workspaceURL

			result, err := kubeLogout(f.in)
			require.NoError(t, err)

			assert.Empty(t, f.closed)
			assert.NoFileExists(t, f.in.store.recordPath(f.record.key()), "the token goes even when the session cannot be closed")
			assertGone(t, path, "alpacon-prod-east")
			assert.Contains(t, warningsOf(result), "was not closed")
		})
	}
}

func TestKubeLogoutCleansEveryFileKubectlReads(t *testing.T) {
	t.Parallel()
	first := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
	second := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	f := newLogoutFixture(t, nil, first, second)

	result, err := kubeLogout(f.in)
	require.NoError(t, err)

	assert.Equal(t, []string{first, second}, result.Kubeconfigs)
	assertGone(t, first, "alpacon-prod-east")
	assertGone(t, second, "alpacon-prod-east")
	assert.Equal(t, "other", result.EffectiveContext)
	assert.Contains(t, warningsOf(result), `kubectl now uses the context "other"`)
}

// Two files can hold one name for two workspaces; each has its own token.
func TestKubeLogoutCoversTheRecordBehindEachFile(t *testing.T) {
	t.Parallel()
	first := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	const otherServer = "https://other-ws.example.com/kube/c-9"
	second := kubeconfigWith(t, "other", [2]string{"prod-east", otherServer})
	f := newLogoutFixture(t, nil, first, second)
	elsewhere := testRecord("prod-east")
	elsewhere.WorkspaceURL = "https://other-ws.example.com"
	elsewhere.ProxyURL = otherServer
	elsewhere.KubeSessionID = "k-9"
	require.NoError(t, f.in.store.write(elsewhere))

	result, err := kubeLogout(f.in)
	require.NoError(t, err)

	assert.Equal(t, []string{"k-1"}, f.closed, "only the logged-in workspace's session can be closed")
	assert.Len(t, result.Sessions, 2)
	assert.NoFileExists(t, f.in.store.recordPath(f.record.key()))
	assert.NoFileExists(t, f.in.store.recordPath(elsewhere.key()))
}

func TestKubeLogoutLeavesARecordOfAnotherWorkspaceTheKubeconfigDoesNotUse(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	f := newLogoutFixture(t, nil, path)
	elsewhere := testRecord("prod-east")
	elsewhere.WorkspaceURL = "https://other-ws.example.com"
	elsewhere.ProxyURL = "https://other-ws.example.com/kube/c-9"
	require.NoError(t, f.in.store.write(elsewhere))

	result, err := kubeLogout(f.in)
	require.NoError(t, err)

	assert.FileExists(t, f.in.store.recordPath(elsewhere.key()))
	assert.Contains(t, warningsOf(result), "Kept the token saved for prod-east in workspace https://other-ws.example.com")
}

func TestKubeLogoutFindingNothingIsNotAnError(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "other")
	store := kubeSessionStore{dir: t.TempDir()}
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	result, err := kubeLogout(kubeLogoutInput{
		cluster: "prod-east", paths: []string{path, filepath.Join(t.TempDir(), "missing")}, store: store, workspaceURL: logoutWorkspace,
		closer: func(kubeSessionRecord, <-chan struct{}) error { t.Error("nothing to close"); return nil },
	})
	require.NoError(t, err)

	assert.False(t, result.Found, "%+v", result)
	assert.Empty(t, result.warnings)
	assert.Equal(t, []string{}, result.Kubeconfigs)
	assert.Equal(t, []loggedOutSession{}, result.Sessions)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a file with nothing to remove is not rewritten")
}

func TestKubeLogoutOfAStaticTokenContextWarnsItsSessionStaysOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	body := "clusters:\n- cluster:\n    server: https://ws.example.com/kube/c-1\n  name: alpacon-prod-east\ncontexts:\n- context:\n    cluster: alpacon-prod-east\n    user: alpacon-prod-east\n  name: alpacon-prod-east\nusers:\n- name: alpacon-prod-east\n  user:\n    token: static\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))

	result, err := kubeLogout(kubeLogoutInput{cluster: "prod-east", paths: []string{path}, store: kubeSessionStore{dir: t.TempDir()}, workspaceURL: logoutWorkspace})
	require.NoError(t, err)

	assert.True(t, result.Found, "%+v", result)
	assert.Contains(t, warningsOf(result), "static token")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "static")
}

func TestKubeLogoutOfARecordWithNoContextNamesKubeconfig(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "other")
	f := newLogoutFixture(t, nil, path)

	result, err := kubeLogout(f.in)
	require.NoError(t, err)

	assert.True(t, result.Found, "%+v", result)
	assert.Equal(t, []string{"k-1"}, f.closed)
	assert.Contains(t, warningsOf(result), "--kubeconfig")
}

// A running login holds the store lock through its approval wait.
func TestKubeLogoutChangesNothingWhileALoginRuns(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
	f := newLogoutFixture(t, nil, path)
	held, err := f.in.store.lock(f.record.key())
	require.NoError(t, err)
	defer func() { _ = held.Release() }()

	_, err = kubeLogout(f.in)

	require.ErrorIs(t, err, errStoreLocked)
	assertHolds(t, path, "alpacon-prod-east")
	assert.FileExists(t, f.in.store.recordPath(f.record.key()))
	assert.Empty(t, f.closed)
}

// A login that finished between the scan and the lock replaced the record, and
// the session it opened is the one to close. The window itself cannot be held
// open from a test, so this exercises the read under the lock directly.
func TestLockRecordsReadsTheRecordAgain(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: t.TempDir()}
	stale := testRecord("prod-east")
	newer := stale
	newer.KubeSessionID = "k-2"
	require.NoError(t, store.write(newer))
	locks := &logoutLocks{}
	defer locks.release()

	records, err := lockRecords(store, locks, []kubeSessionRecord{stale})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "k-2", records[0].KubeSessionID)
}

// A login waiting on its first approval has no record yet, and still holds the
// lock under the key it will write.
func TestKubeLogoutChangesNothingWhileAFirstLoginRuns(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
	store := kubeSessionStore{dir: t.TempDir()}
	held, err := store.lock(testKey("prod-east"))
	require.NoError(t, err)
	defer func() { _ = held.Release() }()

	_, err = kubeLogout(kubeLogoutInput{cluster: "prod-east", paths: []string{path}, store: store, workspaceURL: logoutWorkspace})

	require.ErrorIs(t, err, errStoreLocked)
	assertHolds(t, path, "alpacon-prod-east")
}

func TestKubeLogoutRemovesAStagingFileWithoutARecord(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: t.TempDir()}
	require.NoError(t, store.ensureDir())
	staging := filepath.Join(store.dir, recordBase(testKey("prod-east"))+".123.tmp")
	require.NoError(t, os.WriteFile(staging, []byte("token"), storeFilePerm))

	_, err := kubeLogout(kubeLogoutInput{cluster: "prod-east", paths: []string{kubeconfigWith(t, "other")}, store: store, workspaceURL: logoutWorkspace})

	require.NoError(t, err)
	assert.NoFileExists(t, staging)
}

func TestKubeLogoutFirstInterrupt(t *testing.T) {
	t.Parallel()
	t.Run("before any edit nothing changes", func(t *testing.T) {
		t.Parallel()
		path := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
		f := newLogoutFixture(t, nil, path)
		f.in.interrupted = func() bool { return true }

		_, err := kubeLogout(f.in)

		require.ErrorIs(t, err, errLogoutInterrupted)
		assertHolds(t, path, "alpacon-prod-east")
		assert.FileExists(t, f.in.store.recordPath(f.record.key()))
		assert.NoFileExists(t, path+".lock")
		assert.Empty(t, f.closed)
	})
	t.Run("after an edit the token still goes and the close is skipped", func(t *testing.T) {
		t.Parallel()
		path := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
		f := newLogoutFixture(t, nil, path)
		fired := false
		f.in.interrupted = func() bool { return fired }
		f.in.save = func(ed *kubeconfig.Editor) error {
			fired = true
			return ed.Save()
		}

		result, err := kubeLogout(f.in)

		require.NoError(t, err)
		assertGone(t, path, "alpacon-prod-east")
		assert.NoFileExists(t, f.in.store.recordPath(f.record.key()))
		assert.Empty(t, f.closed)
		assert.Contains(t, warningsOf(result), "Interrupted before closing the kube session k-1")
	})
}

// A current-context of an earlier file was already in effect, so clearing one in
// a later file changes nothing for kubectl and draws no notice.
func TestKubeLogoutSaysNothingWhenTheEffectiveContextStays(t *testing.T) {
	t.Parallel()
	first := kubeconfigWith(t, "other", [2]string{"dev", "https://ws.example.com/kube/c-2"})
	second := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
	f := newLogoutFixture(t, nil, first, second)

	result, err := kubeLogout(f.in)

	require.NoError(t, err)
	assert.True(t, result.CurrentContextCleared, "%+v", result)
	assert.Equal(t, "other", result.EffectiveContext)
	assert.NotContains(t, warningsOf(result), "kubectl now uses")
	assert.False(t, result.currentUnset, "%+v", result)
}

func TestKubeLogoutChangesNothingWhenAFileCannotBeLocked(t *testing.T) {
	t.Parallel()
	free := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	locked := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	held, err := kubeconfig.Open(locked)
	require.NoError(t, err)
	defer func() { _ = held.Close() }()
	f := newLogoutFixture(t, nil, free, locked)

	_, err = kubeLogout(f.in)

	require.ErrorIs(t, err, kubeconfig.ErrLocked)
	assertHolds(t, free, "alpacon-prod-east")
	assert.FileExists(t, f.in.store.recordPath(f.record.key()))
	assert.NoFileExists(t, free+".lock", "the lock taken before the failure is released")
	assert.Empty(t, f.closed)
}

func TestKubeLogoutHandlesAFileItCannotParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		wantFail bool
	}{
		{name: "unrelated file is skipped", body: "clusters: 1\n"},
		{name: "file naming the context fails", body: "clusters: 1\n# alpacon-prod-east\n", wantFail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			good := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
			bad := filepath.Join(t.TempDir(), "bad")
			require.NoError(t, os.WriteFile(bad, []byte(tt.body), 0600))
			f := newLogoutFixture(t, nil, bad, good)

			result, err := kubeLogout(f.in)

			if tt.wantFail {
				require.ErrorContains(t, err, bad)
				assertHolds(t, good, "alpacon-prod-east")
				return
			}
			require.NoError(t, err)
			assertGone(t, good, "alpacon-prod-east")
			assert.Contains(t, warningsOf(result), "Skipping "+bad)
		})
	}
}

// A file saved before another failed stays saved, and the token still goes: it
// must not outlive the context it served.
func TestKubeLogoutFinishesAfterASaveFails(t *testing.T) {
	t.Parallel()
	good := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	bad := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	f := newLogoutFixture(t, nil, good, bad)
	saves := 0
	f.in.save = func(ed *kubeconfig.Editor) error {
		saves++
		if saves == 2 {
			return errors.New("disk full")
		}
		return ed.Save()
	}

	result, err := kubeLogout(f.in)

	require.ErrorContains(t, err, bad)
	require.ErrorContains(t, err, "disk full")
	assert.Equal(t, []string{good}, result.Kubeconfigs)
	assertGone(t, good, "alpacon-prod-east")
	assertHolds(t, bad, "alpacon-prod-east")
	assert.NoFileExists(t, f.in.store.recordPath(f.record.key()))
	assert.Equal(t, []string{"k-1"}, f.closed)
	assert.NoFileExists(t, bad+".lock")
}

func TestClusterOfCurrentContext(t *testing.T) {
	t.Parallel()
	staticBody := "contexts:\n- context: {cluster: alpacon-a, user: alpacon-a}\n  name: alpacon-a\ncurrent-context: alpacon-a\nusers:\n- name: alpacon-a\n  user:\n    token: static\n"
	certBody := "contexts:\n- context: {cluster: alpacon-a, user: alpacon-a}\n  name: alpacon-a\ncurrent-context: alpacon-a\nusers:\n- name: alpacon-a\n  user:\n    client-certificate-data: abc\n"
	mismatchBody := "contexts:\n- context: {cluster: alpacon-a, user: alpacon-a}\n  name: alpacon-a\ncurrent-context: alpacon-a\nusers:\n- name: alpacon-a\n  user:\n    exec:\n      args: [kube, credentials, --cluster, b]\n      command: alpacon\n"
	tests := []struct {
		name  string
		body  string
		want  string
		usage bool
	}{
		{name: "plugin context", body: pluginBody("prod-east"), want: "prod-east"},
		{name: "cluster named with the prefix", body: pluginBody("alpacon-x"), want: "alpacon-x"},
		{name: "static token context", body: staticBody, want: "a"},
		{name: "no current context", body: "current-context: \"\"\n", usage: true},
		{name: "someone else's context", body: "current-context: minikube\n", usage: true},
		{name: "prefix alone proves nothing", body: certBody, usage: true},
		{name: "plugin names another cluster", body: mismatchBody, usage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config")
			require.NoError(t, os.WriteFile(path, []byte(tt.body), 0600))

			got, err := clusterOfCurrentContext([]string{path})

			if tt.usage {
				require.Error(t, err)
				assert.True(t, utils.IsUsageError(err), "naming CLUSTER fixes it, so it exits 2: %v", err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func pluginBody(cluster string) string {
	clusters, contexts, users := pluginEntries(cluster, prodServer)
	return fmt.Sprintf("clusters:\n%scontexts:\n%scurrent-context: %s%s\nusers:\n%s", clusters, contexts, contextPrefix, cluster, users)
}

func TestClusterOfCurrentContextFailsOnAMalformedFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte("clusters: 1\n"), 0600))

	_, err := clusterOfCurrentContext([]string{path})

	require.ErrorIs(t, err, kubeconfig.ErrMalformed)
	assert.False(t, utils.IsUsageError(err), "naming CLUSTER does not fix a broken file, so %v must not be a usage error", err)
}

// A server no record names, as after 'kubectl config set-cluster' moved it,
// still leads to the logged-in workspace's token.
func TestKubeLogoutFallsBackToTheLoginsRecordForAMovedServer(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "other", [2]string{"prod-east", "https://moved.example.com/kube/c-1"})
	f := newLogoutFixture(t, nil, path)

	result, err := kubeLogout(f.in)
	require.NoError(t, err)

	assert.NoFileExists(t, f.in.store.recordPath(f.record.key()))
	assert.Equal(t, []string{"k-1"}, f.closed)
	assert.NotContains(t, warningsOf(result), "No kube session is saved")
}

// --kubeconfig naming a file without the context, a typo say, leaves the token
// that the real context still uses.
func TestKubeLogoutKeepsTheTokenWhenTheNamedFileLacksTheContext(t *testing.T) {
	t.Parallel()
	f := newLogoutFixture(t, nil, kubeconfigWith(t, "other"))
	f.in.explicit = true

	result, err := kubeLogout(f.in)
	require.NoError(t, err)

	assert.False(t, result.Found, "%+v", result)
	assert.FileExists(t, f.in.store.recordPath(f.record.key()))
	assert.Empty(t, f.closed)
	assert.Contains(t, warningsOf(result), "so the token saved for prod-east was kept")
}

func TestKubeLogoutDeletesARecordItCannotRead(t *testing.T) {
	t.Parallel()
	path := kubeconfigWith(t, "other", [2]string{"prod-east", prodServer})
	store := kubeSessionStore{dir: t.TempDir()}
	require.NoError(t, store.ensureDir())
	broken := store.recordPath(testKey("prod-east"))
	require.NoError(t, os.WriteFile(broken, []byte(`{"cluster":"prod-east","channel_token":"secret","token_expired_at":"soon"}`), storeFilePerm))

	result, err := kubeLogout(kubeLogoutInput{cluster: "prod-east", paths: []string{path}, store: store, workspaceURL: logoutWorkspace})
	require.NoError(t, err)

	assert.True(t, result.Found, "%+v", result)
	assert.NoFileExists(t, broken)
	assert.Contains(t, warningsOf(result), "could not be read")
}

func TestKubeLogoutWarnsOfWhatItLeaves(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        string
		wantWarning string
	}{
		{
			name:        "a user of another name holds a token",
			body:        "contexts:\n- context: {cluster: c1, user: u1}\n  name: alpacon-prod-east\nusers:\n- name: u1\n  user: {token: secret}\n",
			wantWarning: `used the user "u1"`,
		},
		{
			name:        "a renamed context names the removed entries",
			body:        "clusters:\n- cluster: {server: 'https://ws.example.com/kube/c-1'}\n  name: alpacon-prod-east\ncontexts:\n- context: {cluster: alpacon-prod-east, user: alpacon-prod-east}\n  name: prod\n",
			wantWarning: `These contexts still name the removed cluster or user of prod-east: "prod"`,
		},
		{
			name:        "a context with no saved session",
			body:        "contexts:\n- context: {cluster: c1, user: u1}\n  name: alpacon-prod-east\n",
			wantWarning: "No kube session is saved for prod-east",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config")
			require.NoError(t, os.WriteFile(path, []byte(tt.body), 0600))

			result, err := kubeLogout(kubeLogoutInput{cluster: "prod-east", paths: []string{path}, store: kubeSessionStore{dir: t.TempDir()}})
			require.NoError(t, err)

			assert.Contains(t, warningsOf(result), tt.wantWarning)
		})
	}
}

// Both files named the context and only the second was saved: kubectl still
// uses the context from the first, which is no change of context to report.
func TestKubeLogoutDoesNotReportTheRemovedContextAsTheNewOne(t *testing.T) {
	t.Parallel()
	first := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
	second := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
	f := newLogoutFixture(t, nil, first, second)
	saves := 0
	f.in.save = func(ed *kubeconfig.Editor) error {
		if saves++; saves == 1 {
			return errors.New("disk full")
		}
		return ed.Save()
	}

	result, err := kubeLogout(f.in)

	require.ErrorContains(t, err, "disk full")
	assert.Equal(t, "alpacon-prod-east", result.EffectiveContext)
	assert.NotContains(t, warningsOf(result), "kubectl now uses")
}

// The token must not outlive the context: a failed delete fails the command,
// and the rest, the close included, still runs.
func TestKubeLogoutReportsATokenItCouldNotDelete(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("a directory mode cannot refuse a delete on Windows")
	}
	path := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
	f := newLogoutFixture(t, nil, path)
	// The lock file exists before the directory turns read-only, so locking
	// still opens it.
	lock, err := f.in.store.lock(f.record.key())
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	require.NoError(t, os.Chmod(f.in.store.dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(f.in.store.dir, storeDirPerm) })

	result, err := kubeLogout(f.in)

	require.ErrorContains(t, err, "the saved token for prod-east was not deleted")
	assert.True(t, result.Found, "%+v", result)
	assertGone(t, path, "alpacon-prod-east")
	assert.Equal(t, []string{"k-1"}, f.closed)
}

func TestQuitLogoutReleasesEveryLockBeforeTheExit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		localDone bool
		wantMsg   string
	}{
		{name: "during the local cleanup", wantMsg: "may be only partly done"},
		{name: "during the close", localDone: true, wantMsg: "Everything on this machine was removed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := kubeSessionStore{dir: t.TempDir()}
			path := kubeconfigWith(t, "other")
			locks := &logoutLocks{}
			require.NoError(t, locks.lockStore(store, testKey("prod-east")))
			_, err := locks.open(path)
			require.NoError(t, err)
			var done atomic.Bool
			done.Store(tt.localDone)

			var msg string
			quitLogout(locks, &done, func(m string) {
				msg = m
				lock, err := store.lock(testKey("prod-east"))
				if assert.NoError(t, err, "the store lock is released before the exit") {
					_ = lock.Release()
				}
				assert.NoFileExists(t, path+".lock", "the kubeconfig lock is released before the exit")
			})()

			assert.Contains(t, msg, tt.wantMsg)
		})
	}
}
