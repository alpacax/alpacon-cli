package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logoutScene is a saved login of prod-east: its record in the store and its
// context in the kubeconfig. Serial: it sets HOME and KUBECONFIG.
type logoutScene struct {
	kubeconfig string
	store      kubeSessionStore
	record     kubeSessionRecord
}

// runKubeCommand runs a fresh kube tree in process under the HOME and
// KUBECONFIG the test set, and reads the exit code cmd.Execute would leave.
// stderr holds what the command printed and then its failure, printed as
// cmd.Execute prints it. Serial: it swaps os.Stdout and os.Stderr and sets
// utils.OutputFormat.
func runKubeCommand(t *testing.T, outputFormat string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runKubeCommandWith(t, client.NewAlpaconAPIClient, outputFormat, args...)
}

func runKubeCommandWith(t *testing.T, newClient clientFactory, outputFormat string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	withOutputFormat(t, outputFormat)
	cmd := newKubeCmd(newClient)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // as RootCmd is
	cmd.SetArgs(args)
	var err error
	stdout, stderr = testutil.CaptureOutput(t, func() { err = cmd.Execute() })
	var failure strings.Builder
	exitCode = reportExit(&failure, err)
	return stdout, stderr + failure.String(), exitCode
}

// apiClientTo builds clients for ts, so a test needs no login in HOME.
func apiClientTo(ts *httptest.Server) clientFactory {
	return func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, nil
	}
}

// reportExit prints err the way cmd.Execute does and returns its exit code.
// An error that is no ExitError is Cobra rejecting the command line, which
// cmd.Execute exits 2 on; that mapping itself is tested in package cmd.
func reportExit(w io.Writer, err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*utils.ExitError](err); ok {
		utils.FprintExitError(w, exitErr)
		return exitErr.Code
	}
	_, _ = fmt.Fprintln(w, err)
	return utils.ExitCodeUsageError
}

func newLogoutScene(t *testing.T, workspaceURL string) logoutScene {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := kubeconfigWith(t, "alpacon-prod-east", [2]string{"prod-east", prodServer})
	t.Setenv("KUBECONFIG", path)
	store, err := defaultKubeSessionStore()
	require.NoError(t, err)
	record := testRecord("prod-east")
	record.WorkspaceURL = workspaceURL
	record.KubeSessionID = "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47"
	require.NoError(t, store.write(record))
	return logoutScene{kubeconfig: path, store: store, record: record}
}

func closeServer(t *testing.T, status int, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		want := "/api/kubernetes/sessions/3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47/close/"
		if r.Method != http.MethodPost || r.URL.Path != want {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestKubeLogoutCommandClosesTheSession(t *testing.T) {
	var calls atomic.Int32
	ts := closeServer(t, http.StatusOK, &calls)
	scene := newLogoutScene(t, ts.URL)
	require.NoError(t, config.CreateConfig(ts.URL, "ws", "token", "", "", "", "", 0, false))

	stdout, stderr, code := runKubeCommand(t, utils.OutputFormatJSON, "logout", "prod-east")

	require.Equal(t, 0, code, stderr)
	assert.JSONEq(t, fmt.Sprintf(`{"cluster":"prod-east","context":"alpacon-prod-east","found":true,"kubeconfigs":[%q],
		"current_context_cleared":true,"effective_context":"",
		"sessions":[{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","workspace":%q,"closed":true,"not_found":false}]}`, scene.kubeconfig, ts.URL), stdout)
	assert.Equal(t, int32(1), calls.Load())
	assertGone(t, scene.kubeconfig, "alpacon-prod-east")
	assert.NoFileExists(t, scene.store.recordPath(scene.record.key()))
	assert.NoFileExists(t, scene.kubeconfig+".lock")
}

func TestKubeLogoutCommandFailedCloseStillExitsZero(t *testing.T) {
	var calls atomic.Int32
	ts := closeServer(t, http.StatusForbidden, &calls)
	scene := newLogoutScene(t, ts.URL)
	require.NoError(t, config.CreateConfig(ts.URL, "ws", "token", "", "", "", "", 0, false))

	_, stderr, code := runKubeCommand(t, utils.OutputFormatTable, "logout")

	require.Equal(t, 0, code, stderr)
	assert.Equal(t, int32(1), calls.Load())
	assert.Contains(t, stderr, "Failed to close the kube session 3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47")
	assertGone(t, scene.kubeconfig, "alpacon-prod-east")
	assert.NoFileExists(t, scene.store.recordPath(scene.record.key()))
}

func TestKubeLogoutCommandWorksLoggedOut(t *testing.T) {
	scene := newLogoutScene(t, logoutWorkspace)

	_, stderr, code := runKubeCommand(t, utils.OutputFormatTable, "logout", "prod-east")

	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stderr, "was not closed")
	assertGone(t, scene.kubeconfig, "alpacon-prod-east")
	assert.NoFileExists(t, scene.store.recordPath(scene.record.key()))
}

func TestKubeLogoutCommandExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		kubeconfig string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "current context is not ours", kubeconfig: "current-context: minikube\n", args: []string{"logout"}, wantCode: utils.ExitCodeUsageError, wantStderr: "not written by alpacon"},
		{name: "blank cluster", kubeconfig: "", args: []string{"logout", " "}, wantCode: utils.ExitCodeUsageError, wantStderr: "CLUSTER must name a cluster"},
		{name: "too many arguments", kubeconfig: "", args: []string{"logout", "a", "b"}, wantCode: utils.ExitCodeUsageError, wantStderr: "accepts at most 1 arg(s)"},
		{name: "a broken file naming the context", kubeconfig: "clusters: 1\n# alpacon-prod-east\n", args: []string{"logout", "prod-east"}, wantCode: utils.ExitCodeGeneralError, wantStderr: "did not finish"},
		{name: "nothing to log out of", kubeconfig: "", args: []string{"logout", "prod-east"}, wantCode: 0, wantStderr: "No kube login for prod-east was found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			path := filepath.Join(t.TempDir(), "config")
			require.NoError(t, os.WriteFile(path, []byte(tt.kubeconfig), 0600))
			t.Setenv("KUBECONFIG", path)

			_, stderr, code := runKubeCommand(t, utils.OutputFormatTable, tt.args...)

			assert.Equal(t, tt.wantCode, code, stderr)
			assert.Contains(t, stderr, tt.wantStderr)
		})
	}
}

// An expected refusal under --output json is one envelope on stderr, with
// nothing on stdout and no "report an issue" footer.
func TestKubeLogoutCommandRefusesWhileTheSessionIsInUse(t *testing.T) {
	scene := newLogoutScene(t, logoutWorkspace)
	held, err := scene.store.lock(scene.record.key())
	require.NoError(t, err)
	defer func() { _ = held.Release() }()

	stdout, stderr, code := runKubeCommand(t, utils.OutputFormatJSON, "logout", "prod-east")

	assert.Equal(t, utils.ExitCodeGeneralError, code, stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, `"operation":"kube_logout"`)
	assert.Contains(t, stderr, "another alpacon command is using the saved kube session")
	assert.NotContains(t, stderr, "report on")
	assertHolds(t, scene.kubeconfig, "alpacon-prod-east")
}

// What a logout that failed partway did is still reported: the result on
// stdout, the failure as an envelope on stderr.
func TestKubeLogoutCommandReportsWhatItDidBeforeAFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory mode cannot refuse a delete on Windows")
	}
	scene := newLogoutScene(t, logoutWorkspace)
	lock, err := scene.store.lock(scene.record.key())
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	require.NoError(t, os.Chmod(scene.store.dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(scene.store.dir, storeDirPerm) })

	stdout, stderr, code := runKubeCommand(t, utils.OutputFormatJSON, "logout", "prod-east")

	assert.Equal(t, utils.ExitCodeGeneralError, code, stderr)
	var result kubeLogoutResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &result), stdout)
	assert.Equal(t, []string{scene.kubeconfig}, result.Kubeconfigs)
	assert.Contains(t, stderr, "the saved token for prod-east was not deleted")
}

// A first Ctrl+C while the server is closing the session gives up on the
// close, and the command still prints its result and exits 0: everything on
// this machine is already gone.
func TestKubeLogoutCommandFirstInterruptDuringTheClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot send an interrupt on Windows")
	}
	asked := make(chan struct{}, 1)
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked <- struct{}{}
		<-release
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(release) })
	scene := newLogoutScene(t, ts.URL)
	require.NoError(t, config.CreateConfig(ts.URL, "ws", "token", "", "", "", "", 0, false))

	// The watcher has taken SIGINT by the time the close is sent, so the
	// signal reaches it rather than ending the test binary.
	go func() {
		select {
		case <-asked:
			self, err := os.FindProcess(os.Getpid())
			if err == nil {
				err = self.Signal(os.Interrupt)
			}
			if err != nil {
				t.Errorf("interrupt: %v", err)
			}
		case <-t.Context().Done():
		}
	}()
	stdout, stderr, code := runKubeCommand(t, utils.OutputFormatJSON, "logout", "prod-east")

	require.Equal(t, 0, code, stderr)
	var result kubeLogoutResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &result), stdout)
	assert.Equal(t, []loggedOutSession{{ID: scene.record.KubeSessionID, Workspace: ts.URL, CloseError: "interrupted"}}, result.Sessions)
	assert.Contains(t, stderr, "Interrupted before closing the kube session")
	assertGone(t, scene.kubeconfig, "alpacon-prod-east")
	assert.NoFileExists(t, scene.store.recordPath(scene.record.key()))
}

func TestKubeSessionsCommandRefusals(t *testing.T) {
	const notEnabled = "Kubernetes support is not enabled"
	tests := []struct {
		name       string
		clusters   int // status of the cluster lookup
		sessions   int // status of the session list
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "surface off on the list", sessions: http.StatusNotFound, args: []string{"sessions"}, wantCode: 1, wantStderr: notEnabled},
		{name: "surface off on the lookup", clusters: http.StatusNotFound, args: []string{"sessions", "--cluster", "prod-east"}, wantCode: 1, wantStderr: notEnabled},
		{name: "a bad filter", sessions: http.StatusBadRequest, args: []string{"sessions"}, wantCode: 1, wantStderr: "Failed to retrieve the kube sessions"},
		{name: "unknown cluster", clusters: http.StatusOK, args: []string{"sessions", "--cluster", "missing"}, wantCode: 1, wantStderr: "Failed to find the cluster"},
		{name: "blank cluster", args: []string{"sessions", "--cluster", " "}, wantCode: utils.ExitCodeUsageError, wantStderr: "--cluster must name a cluster"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/kubernetes/clusters/":
					w.WriteHeader(tt.clusters)
					_, _ = fmt.Fprint(w, `{"count":0,"current":1,"next":null,"previous":null,"last":1,"results":[]}`)
				case "/api/kubernetes/sessions/":
					w.WriteHeader(tt.sessions)
					_, _ = fmt.Fprint(w, `{"code":"invalid"}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer ts.Close()

			_, stderr, code := runKubeCommandWith(t, apiClientTo(ts), utils.OutputFormatTable, tt.args...)

			assert.Equal(t, tt.wantCode, code, stderr)
			assert.Contains(t, stderr, tt.wantStderr)
		})
	}
}

// Serial: it sets HOME.
func TestCloseSavedSessionRefusesAMovedLogin(t *testing.T) {
	var calls atomic.Int32
	ts := closeServer(t, http.StatusOK, &calls)
	setupKubeCommandConfig(t, ts.URL)
	record := testRecord("prod-east")
	record.WorkspaceURL = "https://elsewhere.example.com"

	err := closeSavedSession(record, nil)

	require.ErrorContains(t, err, "moved to another workspace")
	assert.Zero(t, calls.Load())
}

func TestWithin(t *testing.T) {
	t.Parallel()
	t.Run("gives up at the timeout", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			block := make(chan struct{})
			defer close(block)
			start := time.Now()
			_, err := within(closeTimeout, nil, func() (int, error) { <-block; return 0, nil })
			require.ErrorIs(t, err, errNoAnswer)
			assert.Equal(t, closeTimeout, time.Since(start))
		})
	})
	t.Run("gives up once cancelled", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			block, cancel := make(chan struct{}), make(chan struct{})
			defer close(block)
			start := time.Now()
			go func() { time.Sleep(time.Second); close(cancel) }()
			_, err := within(closeTimeout, cancel, func() (int, error) { <-block; return 0, nil })
			require.ErrorIs(t, err, errCallInterrupted)
			assert.Equal(t, time.Second, time.Since(start))
		})
	})
	t.Run("returns the answer", func(t *testing.T) {
		t.Parallel()
		want := errors.New("refused")
		got, err := within(closeTimeout, nil, func() (int, error) { return 7, want })
		require.ErrorIs(t, err, want)
		assert.Equal(t, 7, got)
	})
}
