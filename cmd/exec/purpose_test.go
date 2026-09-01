package exec

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	execPurposeHelperMarker = "--exec-purpose-helper--"
	purposeJobID            = "a1b2c3d4-5678-abcd-ef01-234567890abc"
)

// TestExecPurposeHelperProcess runs `exec purpose` in the child process, the way
// TestExecLogsHelperProcess does for `exec logs`.
func TestExecPurposeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXEC_PURPOSE_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, execPurposeHelperMarker)
	if !ok {
		t.Fatal("missing " + execPurposeHelperMarker + " marker")
	}
	purposeCmd.Run(purposeCmd, args)
}

func runExecPurposeHelper(t *testing.T, workspaceURL string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, workspaceURL,
		"TestExecPurposeHelperProcess", execPurposeHelperMarker,
		[]string{"GO_WANT_EXEC_PURPOSE_HELPER=1"}, args)
}

// runExecPurposeOK is runExecPurposeHelper for the success path, which
// runHelperProcess cannot serve: it requires a non-zero exit.
func runExecPurposeOK(t *testing.T, home string, args ...string) string {
	t.Helper()
	helper := osexec.Command(os.Args[0],
		append([]string{"-test.run=^TestExecPurposeHelperProcess$", "--", execPurposeHelperMarker}, args...)...)
	helper.Env = append(os.Environ(),
		"ALPACON_WORK_SESSION=", "HOME="+home, "GO_WANT_EXEC_PURPOSE_HELPER=1")
	var outBuf, errBuf bytes.Buffer
	helper.Stdout = &outBuf
	helper.Stderr = &errBuf
	require.NoError(t, helper.Run(), "stderr: %s", errBuf.String())
	return errBuf.String()
}

func longPurpose() string {
	b := make([]rune, PurposeMaxLength+1)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

// TestExecPurposeRejectsBadArgsWithExit1 pins the exit code for this command's
// own argument validation. Exit 2 is reserved for work-session, event
// wait/watch, and utils.RequirePositiveInt (README "Exit codes"); every other
// command exits 1, `exec logs` included. Exiting 2 here would tell a script this
// is a usage-error-only surface, which it is not.
func TestExecPurposeRejectsBadArgsWithExit1(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"not a uuid", []string{"not-a-uuid", "clock skew"}, "invalid JOB_ID"},
		{"blank purpose", []string{purposeJobID, "   "}, "PURPOSE requires a value"},
		{"over the ceiling", []string{purposeJobID, longPurpose()}, "limited to 2000 characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, exitCode := runExecPurposeHelper(t, ts.URL, tc.args...)
			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assert.Contains(t, stderr, tc.want)
		})
	}
}

// TestExecPurposeAcceptsAMultibytePurposeAtTheCeiling is the byte-vs-rune
// regression. The server counts characters (DRF CharField(max_length=2000)); a
// byte count refuses a Korean purpose at roughly 666 of them, while claiming the
// server would refuse it.
func TestExecPurposeAcceptsAMultibytePurposeAtTheCeiling(t *testing.T) {
	korean := make([]rune, PurposeMaxLength)
	for i := range korean {
		korean[i] = '한'
	}

	var body atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/"+purposeJobID+"/purpose/" {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			body.Store(payload)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)
	runExecPurposeOK(t, home, purposeJobID, string(korean))

	sent, ok := body.Load().(map[string]any)
	require.True(t, ok, "a 2000-character Korean purpose was refused locally")
	assert.Equal(t, string(korean), sent["purpose"])
}

// TestExecPurposeAnswersTheDemand covers the happy path: a 202 and a pointer at
// where to read the outcome, never at a resubmit.
func TestExecPurposeAnswersTheDemand(t *testing.T) {
	var body atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/"+purposeJobID+"/purpose/" {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			body.Store(payload)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)
	stderr := runExecPurposeOK(t, home, purposeJobID, "chronyd drifted 40s.")

	sent, ok := body.Load().(map[string]any)
	require.True(t, ok, "no purpose answer captured")
	assert.Equal(t, "chronyd drifted 40s.", sent["purpose"])
	assert.Contains(t, stderr, "Purpose recorded")
	assert.Contains(t, stderr, "alpacon exec logs "+purposeJobID)
	assert.NotContains(t, stderr, "re-run")
}

// TestExecPurposeTrimsBeforeSending pins that the value checked is the value
// sent: `--purpose '  x  '` must not be stored, judged, and written to the
// approver's card with the padding intact.
func TestExecPurposeTrimsBeforeSending(t *testing.T) {
	var body atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/"+purposeJobID+"/purpose/" {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			body.Store(payload)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)
	runExecPurposeOK(t, home, purposeJobID, "  clock skew  ")

	sent, ok := body.Load().(map[string]any)
	require.True(t, ok, "no purpose answer captured")
	assert.Equal(t, "clock skew", sent["purpose"])
}

// TestExecLogsShowsTheStatedPurpose gives EventDetails.Purpose its reader. A
// field parsed into nothing is a promise no surface keeps.
func TestExecLogsShowsTheStatedPurpose(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+purposeJobID+"/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      purposeJobID,
				"status":  "success",
				"success": true,
				"result":  "ok",
				// Server-supplied text written by one principal, printed to
				// another's terminal—so the escape must not survive (#364).
				"purpose": "chronyd drifted 40s\x1b[31m",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)
	helper := osexec.Command(os.Args[0],
		"-test.run=^TestExecLogsHelperProcess$", "--", execLogsHelperMarker, purposeJobID)
	helper.Env = append(os.Environ(),
		"ALPACON_WORK_SESSION=", "HOME="+home, "GO_WANT_EXEC_LOGS_HELPER=1")
	var outBuf, errBuf bytes.Buffer
	helper.Stdout = &outBuf
	helper.Stderr = &errBuf
	require.NoError(t, helper.Run(), "stderr: %s", errBuf.String())

	assert.Contains(t, errBuf.String(), "Stated purpose: chronyd drifted 40s")
	assert.NotContains(t, errBuf.String(), "\x1b[31m")
	// stdout stays the command's own output.
	assert.Contains(t, outBuf.String(), "ok")
	assert.NotContains(t, outBuf.String(), "Stated purpose")
}
