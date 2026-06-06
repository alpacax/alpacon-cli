package exec

import (
	"bytes"
	"encoding/json"
	"errors"
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
// always answers an exec command with a SUDO_APPROVAL_REQUIRED denial
// (success=false + the plugin denial line), so the command stays pending.
func newApprovalDenialServer() *httptest.Server {
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
				"result":      approvalDenialResult,
				"error_phase": nil,
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestExecPendingApprovalExits4WithJSONSignal(t *testing.T) {
	ts := newApprovalDenialServer()
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
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T", err)
	assert.Equal(t, utils.ExitCodePendingApproval, exitErr.ExitCode(), "pending approval must exit 4")

	var got struct {
		OK          bool     `json:"ok"`
		Status      string   `json:"status"`
		ExitCode    int      `json:"exit_code"`
		NextActions []string `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec prod -- sudo reboot", got.NextActions[0], "re-run hint should reconstruct the invocation")
}
