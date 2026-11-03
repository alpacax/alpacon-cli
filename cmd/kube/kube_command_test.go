package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const kubeCommandHelperMarker = "kube-command-helper"

// helperProcessTimeout bounds a child that blocks, so the suite does not wait
// out go test's own timeout.
const helperProcessTimeout = 60 * time.Second

// logoutScene is a saved login of prod-east: its record in the store and its
// context in the kubeconfig. Serial: it sets HOME and KUBECONFIG.
type logoutScene struct {
	kubeconfig string
	store      kubeSessionStore
	record     kubeSessionRecord
}

// TestKubeCommandHelperProcess runs KubeCmd in a child, so these tests can read
// the exit code the command's os.Exit leaves. Run by the parent it returns at once.
func TestKubeCommandHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_KUBE_COMMAND_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == kubeCommandHelperMarker {
			utils.OutputFormat = os.Getenv("GO_KUBE_HELPER_OUTPUT")
			KubeCmd.SetArgs(os.Args[i+1:])
			if err := KubeCmd.Execute(); err != nil {
				os.Exit(utils.ExitCodeUsageError)
			}
			os.Exit(0)
		}
	}
	os.Exit(99)
}

// runKubeCommand runs the command under the HOME and KUBECONFIG the test set.
func runKubeCommand(t *testing.T, outputFormat string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), helperProcessTimeout)
	defer cancel()
	helper := kubeCommandHelper(ctx, outputFormat, args...)
	var out, errOut bytes.Buffer
	helper.Stdout, helper.Stderr = &out, &errOut
	exitCode = waitHelper(t, ctx, helper.Run())
	return out.String(), errOut.String(), exitCode
}

func kubeCommandHelper(ctx context.Context, outputFormat string, args ...string) *osexec.Cmd {
	helper := osexec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestKubeCommandHelperProcess$", "--", kubeCommandHelperMarker}, args...)...)
	helper.Env = append(os.Environ(), "GO_WANT_KUBE_COMMAND_HELPER=1", "GO_KUBE_HELPER_OUTPUT="+outputFormat)
	return helper
}

// waitHelper turns the child's end into its exit code. Cobra's own errors
// reach the helper as a returned error, which it maps to exit 2 the way
// cmd.Execute does; that mapping itself is tested in package cmd.
func waitHelper(t *testing.T, ctx context.Context, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	require.NoError(t, ctx.Err(), "the helper did not finish within %s", helperProcessTimeout)
	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ExitCode()
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
		{name: "too many arguments", kubeconfig: "", args: []string{"logout", "a", "b"}, wantCode: utils.ExitCodeUsageError},
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

	ctx, cancel := context.WithTimeout(t.Context(), helperProcessTimeout)
	defer cancel()
	helper := kubeCommandHelper(ctx, utils.OutputFormatJSON, "logout", "prod-east")
	var out, errOut bytes.Buffer
	helper.Stdout, helper.Stderr = &out, &errOut
	require.NoError(t, helper.Start())
	select {
	case <-asked:
	case <-ctx.Done():
		t.Fatalf("the close was never sent: %s", errOut.String())
	}
	require.NoError(t, helper.Process.Signal(os.Interrupt))
	code := waitHelper(t, ctx, helper.Wait())

	require.Equal(t, 0, code, errOut.String())
	var result kubeLogoutResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	assert.Equal(t, []loggedOutSession{{ID: scene.record.KubeSessionID, Workspace: ts.URL, CloseError: "interrupted"}}, result.Sessions)
	assert.Contains(t, errOut.String(), "Interrupted before closing the kube session")
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
			setupKubeCommandConfig(t, ts.URL)

			_, stderr, code := runKubeCommand(t, utils.OutputFormatTable, tt.args...)

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
