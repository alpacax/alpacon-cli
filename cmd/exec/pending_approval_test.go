package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
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

// TestExecStatusAwaitingApprovalExits4WithJSONSignal drives the status-level HITL
// hold (server status "awaiting_approval", no denial line) through the real exec
// command and asserts it converges on the same pending-approval contract as the
// denial-code path: exit 4 and a {"status":"pending_approval", ...} envelope.
func TestExecStatusAwaitingApprovalExits4WithJSONSignal(t *testing.T) {
	ts := newAwaitingApprovalServer()
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)

	helper := osexec.Command(
		os.Args[0],
		"-test.run=^TestExecCommandWorkSessionGateHelperProcess$",
		"--",
		"exec-worksession-helper",
		"--output",
		"json",
		"prod",
		"--",
		"sudo",
		"reboot",
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_EXEC_WORKSESSION_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var stdout, stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodePendingApproval, exitErr.ExitCode(), "pending approval must exit 4")

	var got struct {
		OK          bool               `json:"ok"`
		Status      string             `json:"status"`
		ExitCode    int                `json:"exit_code"`
		NextActions []utils.NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec logs cmd-1", got.NextActions[0].Command, "hint should point at exec logs for the held job")
}

func TestExecPendingApprovalExits4WithJSONSignal(t *testing.T) {
	ts := newApprovalDenialServer(denialLine("SUDO_APPROVAL_REQUIRED"))
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)

	helper := osexec.Command(
		os.Args[0],
		"-test.run=^TestExecCommandWorkSessionGateHelperProcess$",
		"--",
		"exec-worksession-helper",
		"--output",
		"json",
		"prod",
		"--",
		"sudo",
		"reboot",
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_EXEC_WORKSESSION_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var stdout, stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodePendingApproval, exitErr.ExitCode(), "pending approval must exit 4")

	var got struct {
		OK          bool               `json:"ok"`
		Status      string             `json:"status"`
		ExitCode    int                `json:"exit_code"`
		NextActions []utils.NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec prod -- sudo reboot", got.NextActions[0].Command, "re-run hint should reconstruct the invocation")

	// The pending message already says to re-run after approval, and this code
	// offers nothing past that wait, so printing its hint would say it twice.
	assert.NotContains(t, stderr.String(), "Hint:")
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

// runIntentDeviationHelper drives the real exec command against a server that
// answers with an intent-deviation denial, and returns its stdout and stderr
// after asserting the pending-approval exit code. extraArgs go before the
// server name.
func runIntentDeviationHelper(t *testing.T, extraArgs ...string) (stdout, stderr string) {
	t.Helper()

	ts := newApprovalDenialServer(denialLine("SUDO_INTENT_DEVIATION"))
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)

	args := []string{
		"-test.run=^TestExecCommandWorkSessionGateHelperProcess$",
		"--",
		"exec-worksession-helper",
		"--output",
		"json",
	}
	args = append(args, extraArgs...)
	args = append(args, "prod", "--", "sudo", "reboot")

	helper := osexec.Command(os.Args[0], args...)
	helper.Env = append(os.Environ(),
		"GO_WANT_EXEC_WORKSESSION_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var outBuf, errBuf bytes.Buffer
	helper.Stdout = &outBuf
	helper.Stderr = &errBuf

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodePendingApproval, exitErr.ExitCode(), "pending approval must exit 4; stderr: %s", errBuf.String())

	return outBuf.String(), errBuf.String()
}
