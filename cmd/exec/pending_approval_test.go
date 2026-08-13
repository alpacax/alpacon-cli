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

// approvalDenialResult is the plugin's exact terminal denial line for a sudo
// command that needs human approval.
const approvalDenialResult = "Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n"

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
	ts := newApprovalDenialServer(approvalDenialResult)
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
}

// TestExecIntentDeviationPrintsSelfServiceHint pins the denial-code hint to the
// pending-approval path, which exits 4 before HandleCommandResult. Without an
// explicit print, the reviewer-free way out of an intent deviation (re-declaring
// the session intent) never reaches the user.
func TestExecIntentDeviationPrintsSelfServiceHint(t *testing.T) {
	ts := newApprovalDenialServer("Alpacon denied this sudo command (SUDO_INTENT_DEVIATION).\n")
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

	assert.Contains(t, stderr.String(), "work-session update [SESSION_ID] --title")
	assert.Contains(t, stderr.String(), "may need an approval of its own")

	// The hint rides on stderr, so the machine signal on stdout stays parseable.
	var got struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
}
