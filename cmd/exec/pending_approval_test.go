package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// newApprovalDenialServer returns a test server that resolves one server and
// always answers an exec command with the given plugin denial line
// (success=false), so the command stays pending.
func newApprovalDenialServer(denialLine string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			resp := map[string]any{
				"id":          "cmd-1",
				"status":      "completed",
				"success":     false,
				"exit_code":   1,
				"result":      denialLine,
				"error_phase": nil,
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
}

// newAwaitingApprovalServer returns a test server whose exec command parks at the
// server-side "awaiting_approval" status (HITL hold), so the command is pending
// human approval without ever producing a denial line.
func newAwaitingApprovalServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cmd-1",
				"status": "awaiting_approval",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// pendingApprovalSignal is the machine-readable half of the pending-approval
// contract these tests assert on.
type pendingApprovalSignal struct {
	OK          bool               `json:"ok"`
	Status      string             `json:"status"`
	ExitCode    int                `json:"exit_code"`
	RequestID   string             `json:"request_id"`
	NextActions []utils.NextAction `json:"next_actions"`
}

// runExecHelper runs the exec command tree in a subprocess against workspaceURL
// and returns its stdout, stderr, and exit code. Every caller asserts a refusal,
// so a clean exit fails the test rather than returning 0.
func runExecHelper(t *testing.T, workspaceURL string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, workspaceURL,
		"TestExecCommandWorkSessionGateHelperProcess", execWorkSessionHelperMarker,
		[]string{"GO_WANT_EXEC_WORKSESSION_HELPER=1"}, args)
}

// runExecLogsHelper is runExecHelper for `exec logs`, whose output format rides
// on an env var—see TestExecLogsHelperProcess.
func runExecLogsHelper(t *testing.T, workspaceURL, outputFormat string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, workspaceURL,
		"TestExecLogsHelperProcess", execLogsHelperMarker,
		[]string{"GO_WANT_EXEC_LOGS_HELPER=1", "ALPACON_TEST_OUTPUT_FORMAT=" + outputFormat}, args)
}

// runExecWaitHelper is runExecHelper with the approval-wait poll interval cut to
// milliseconds, so a test that drives the wait loop does not pay the 5s default
// per tick.
func runExecWaitHelper(t *testing.T, workspaceURL string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, workspaceURL,
		"TestExecCommandWorkSessionGateHelperProcess", execWorkSessionHelperMarker,
		[]string{"GO_WANT_EXEC_WORKSESSION_HELPER=1", "ALPACON_TEST_POLL_INTERVAL=10ms"}, args)
}

func runHelperProcess(t *testing.T, workspaceURL, testName, marker string, env, args []string) (stdout, stderr string, exitCode int) {
	t.Helper()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, workspaceURL)

	helper := osexec.Command(os.Args[0],
		append([]string{"-test.run=^" + testName + "$", "--", marker}, args...)...)
	helper.Env = append(os.Environ(), "ALPACON_WORK_SESSION=", "HOME="+home)
	helper.Env = append(helper.Env, env...)
	var outBuf, errBuf bytes.Buffer
	helper.Stdout = &outBuf
	helper.Stderr = &errBuf

	err := helper.Run()
	require.Error(t, err, "helper exited 0; stderr: %s", errBuf.String())
	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return outBuf.String(), errBuf.String(), exitErr.ExitCode()
}

// TestExecStatusAwaitingApprovalExits4WithJSONSignal drives the status-level HITL
// hold (server status "awaiting_approval", no denial line) through the real exec
// command and asserts it converges on the same pending-approval contract as the
// denial-code path: exit 4 and a {"status":"pending_approval", ...} envelope.
func TestExecStatusAwaitingApprovalExits4WithJSONSignal(t *testing.T) {
	t.Parallel()
	ts := newAwaitingApprovalServer()
	defer ts.Close()

	stdout, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "pending approval must exit 4")

	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec logs cmd-1", got.NextActions[0].Command, "hint should point at exec logs for the held job")
}

func TestExecPendingApprovalExits4WithJSONSignal(t *testing.T) {
	t.Parallel()
	ts := newApprovalDenialServer(denialLine("SUDO_APPROVAL_REQUIRED"))
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "pending approval must exit 4")

	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec prod -- sudo reboot", got.NextActions[0].Command, "re-run hint should reconstruct the invocation")

	// The pending message already says to re-run after approval, and this code
	// offers nothing past that wait, so printing its hint would say it twice.
	assert.NotContains(t, stderr, "Hint:")
}

// TestExecIntentDeviationPrintsSelfServiceHint pins the denial-code hint to the
// pending-approval path, which exits 4 before HandleCommandResult. Without an
// explicit print, the reviewer-free way out of an intent deviation (re-declaring
// the session intent) never reaches the user.
func TestExecIntentDeviationPrintsSelfServiceHint(t *testing.T) {
	stdout, stderr := runIntentDeviationHelper(t)

	assert.Contains(t, stderr, "work-session update [SESSION_ID] --title")
	assert.Contains(t, stderr, "may need an approval of its own")

	// The hint rides on stderr, so the machine signal on stdout stays parseable.
	var got struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
}

// TestExecIntentDeviationPrintsSelfServiceHintAfterWaitTimeout covers the other
// way into HandlePendingApproval: a --wait that ran out of window returns the
// last denial to the same handler, so the self-service hint must survive the
// wait rather than only reaching the user on the no-wait path.
func TestExecIntentDeviationPrintsSelfServiceHintAfterWaitTimeout(t *testing.T) {
	// Shorter than the poll interval, so the window closes on the timer without
	// the loop re-attempting the command.
	_, stderr := runIntentDeviationHelper(t, "--wait-approval", "1ms")

	assert.Contains(t, stderr, "Approval wait timed out")
	assert.Contains(t, stderr, "work-session update [SESSION_ID] --title")
}

// TestExecWaitTimeoutCarriesTheApprovalRequestID drives a real --wait-approval
// timeout through the subprocess and checks that the request id the wait loop
// picked up while polling reaches the JSON envelope, not just the in-process
// RunExecWithApprovalWait return value (TestRunExecWithApprovalWait_TimeoutCarriesTheApprovalRequestID).
func TestExecWaitTimeoutCarriesTheApprovalRequestID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                       "cmd-1",
				"status":                   "completed",
				"success":                  false,
				"exit_code":                1,
				"result":                   denialLine("SUDO_APPROVAL_REQUIRED"),
				"sudo_approval_request_id": "req-9",
				"sudo_grant_status":        "pending_approval",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	stdout, _, exitCode := runExecWaitHelper(t, ts.URL, "--output", "json", "--wait-approval", "30ms", "prod", "--", "sudo", "reboot")
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)

	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, "req-9", got.RequestID)
}

// runIntentDeviationHelper drives the real exec command against a server that
// answers with an intent-deviation denial, and returns its stdout and stderr
// after asserting the pending-approval exit code. extraArgs go before the
// server name.
func runIntentDeviationHelper(t *testing.T, extraArgs ...string) (stdout, stderr string) {
	t.Helper()

	ts := newApprovalDenialServer(denialLine("SUDO_INTENT_DEVIATION"))
	defer ts.Close()

	args := append([]string{"--output", "json"}, extraArgs...)
	args = append(args, "prod", "--", "sudo", "reboot")

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, args...)
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "pending approval must exit 4; stderr: %s", stderr)

	return stdout, stderr
}

const execLogsHelperMarker = "exec-logs-helper"

// TestExecLogsHelperProcess runs `exec logs` in a subprocess so a test can read
// its exit code. The output format arrives as an env var: --output lives on
// RootCmd, which this helper does not build.
func TestExecLogsHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXEC_LOGS_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, execLogsHelperMarker)
	if !ok {
		fmt.Fprintln(os.Stderr, "missing "+execLogsHelperMarker+" marker")
		os.Exit(2)
	}
	utils.OutputFormat = os.Getenv("ALPACON_TEST_OUTPUT_FORMAT")
	logsCmd.Run(logsCmd, args)
}

// Exit 0 would tell a consumer reading the code alone that the command finished.
func TestExecLogsAwaitingApprovalExits4(t *testing.T) {
	t.Parallel()
	const jobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+jobID+"/" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": jobID, "status": "awaiting_approval"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	t.Run("table", func(t *testing.T) {
		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, jobID)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "a held job is pending, not finished")
		assert.Contains(t, stderr, "Approval required")
	})

	t.Run("json", func(t *testing.T) {
		stdout, _, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, jobID)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)

		var got pendingApprovalSignal
		require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
		assert.False(t, got.OK)
		assert.Equal(t, utils.PendingApprovalStatus, got.Status)
		assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
		require.NotEmpty(t, got.NextActions)
		assert.Equal(t, "alpacon exec logs "+jobID, got.NextActions[0].Command)
	})
}

// The sibling exit-6 paths (exec, work-session create) answer --output json with
// an error envelope, so prose here would break a consumer polling exec logs.
func TestExecLogsRejectedExits6(t *testing.T) {
	t.Parallel()
	const jobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+jobID+"/" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": jobID, "status": "rejected"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	t.Run("table", func(t *testing.T) {
		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, jobID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "a rejection is final; exit 1 reads as retryable")
		assert.Contains(t, stderr, "rejected by a reviewer")
	})

	t.Run("json", func(t *testing.T) {
		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, jobID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode)

		var got struct {
			OK       bool   `json:"ok"`
			ExitCode int    `json:"exit_code"`
			Message  string `json:"message"`
		}
		require.NoError(t, json.Unmarshal([]byte(stderr), &got), "stderr: %s", stderr)
		assert.False(t, got.OK)
		assert.Equal(t, utils.ExitCodeNotApproved, got.ExitCode)
		assert.Contains(t, got.Message, "rejected by a reviewer")
	})
}

// Exit 1 reads as transient, so an agent re-runs a rejected command forever.
func TestExecRejectedExits6(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cmd-1", "status": "rejected"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "sudo", "reboot")
	assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
	assert.Contains(t, stderr, "rejected by a reviewer")
}

// TestExecRejectedMidWaitExits6 traces the wait loop's rejection branch out to
// process exit: TestRunExecWithApprovalWait_RejectionMidWaitEndsTheWait stops at
// the returned error, and TestExecRejectedExits6 enters through the no-wait path.
func TestExecRejectedMidWaitExits6(t *testing.T) {
	t.Parallel()
	var detailReads atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			// The first read is the submission's own completion poll: denied with
			// an approval request in flight, so --wait enters the loop. A reviewer
			// rejects it while the loop is waiting, seen on the next detail read.
			resp := map[string]any{
				"id":        "cmd-1",
				"status":    "completed",
				"success":   false,
				"exit_code": 1,
				"result":    denialLine("SUDO_APPROVAL_REQUIRED"),
			}
			if detailReads.Add(1) > 1 {
				resp["sudo_grant_status"] = "rejected"
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	_, stderr, exitCode := runExecWaitHelper(t, ts.URL, "--wait-approval", "30s", "prod", "--", "sudo", "reboot")

	assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "a mid-wait rejection is final; stderr: %s", stderr)
	assert.Contains(t, stderr, "rejected by a reviewer")
}
