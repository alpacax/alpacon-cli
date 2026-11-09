package exec

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	heldRequestUUID = "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47"
	heldStatusPath  = "/api/events/commands/cmd-1/"
	heldDeadline    = "2999-11-06T17:20:00+09:00"
)

// newHeldDenialServer answers an exec command with a sudo approval denial whose
// detail carries held, or no held key at all when held is nil.
func newHeldDenialServer(held map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			resp := map[string]any{
				"id":        "cmd-1",
				"status":    "completed",
				"success":   false,
				"exit_code": 1,
				"result":    denialLine("SUDO_APPROVAL_REQUIRED"),
			}
			if held != nil {
				resp["held"] = held
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
}

func fullHeld() map[string]any {
	return map[string]any{"approval_request": heldRequestUUID, "expires_at": heldDeadline, "status_path": heldStatusPath}
}

var (
	reRunAction   = utils.NextAction{Command: "alpacon exec prod -- sudo reboot"}
	consoleAction = utils.NextAction{Description: "Approve it out of band in the Alpacon console (web/Slack). Where supported, pass --wait (or --wait-approval DURATION for a longer timeout) on the original command to block until approval."}
	heldActions   = []utils.NextAction{
		{Description: "Approval request: " + heldRequestUUID},
		{Description: "The approval must be decided by " + heldDeadline},
		{Command: "alpacon api " + heldStatusPath, Description: "Where to read the command's sudo_grant_status while waiting; once it reads authorized, re-run the command"},
	}
)

func TestExecPendingApproval_HeldReachesTheJSONEnvelope(t *testing.T) {
	t.Parallel()
	ts := newHeldDenialServer(fullHeld())
	defer ts.Close()

	stdout, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, heldRequestUUID, got.RequestID)
	assert.Equal(t, append([]utils.NextAction{reRunAction, consoleAction}, heldActions...), got.NextActions)
}

func TestExecPendingApproval_HeldIsPrintedInTableOutput(t *testing.T) {
	t.Parallel()
	ts := newHeldDenialServer(fullHeld())
	defer ts.Close()

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "sudo", "reboot")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
	for _, action := range heldActions {
		assert.Contains(t, stderr, "  "+action.PlainText()+"\n")
	}
}

// An older server sends no held key; the envelope keeps the actions it had before.
func TestExecPendingApproval_NoHeldKeyKeepsTodaysOutput(t *testing.T) {
	t.Parallel()
	ts := newHeldDenialServer(nil)
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Empty(t, got.RequestID)
	assert.Equal(t, []utils.NextAction{reRunAction, consoleAction}, got.NextActions)
	assert.NotContains(t, stderr, "alpacon api")
}

func TestExecPendingApproval_BadStatusPathIsDropped(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, path string }{
		{"dot-dot segment", "/api/events/../servers/"},
		{"absolute URL", "https://evil.example.com/api/events/commands/cmd-1/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			held := fullHeld()
			held["status_path"] = tc.path
			ts := newHeldDenialServer(held)
			defer ts.Close()

			stdout, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")

			assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
			var got pendingApprovalSignal
			require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
			assert.Equal(t, append([]utils.NextAction{reRunAction, consoleAction}, heldActions[:2]...), got.NextActions)
		})
	}
}

// After a cancel the detail reads held: null while the grant is still
// pending_approval; only sudo_grant_status ends the wait, and the cleared hold
// is not reported as still open.
func TestRunExecWithApprovalWait_NullHeldAfterCancelDoesNotEndTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		denial := stubApprovalWaitSeams(t, 10*time.Millisecond, "SUDO_APPROVAL_REQUIRED")
		denial.Held = &utils.HeldRequest{ApprovalRequest: heldRequestUUID, StatusPath: heldStatusPath}
		submits := 0
		runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
			submits++
			return denial
		}
		polls := 0
		getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
			polls++
			status, requestID := "pending_approval", heldRequestUUID
			return event.EventDetails{SudoGrantStatus: &status, SudoApprovalRequestID: &requestID}, nil
		}

		err := RunExecWithApprovalWait(nil, "srv", "sudo reboot", "", "", nil, "", "", 80*time.Millisecond, io.Discard)

		var remoteErr *event.RemoteCommandError
		require.ErrorAs(t, err, &remoteErr)
		assert.Equal(t, 1, submits)
		assert.Greater(t, polls, 1)
		assert.Equal(t, heldRequestUUID, remoteErr.ApprovalRequestID)
		assert.Nil(t, remoteErr.Held)
		assert.NotNil(t, denial.Held, "the caller's denial is copied, not changed")
	})
}

func TestRunExecWithApprovalWait_TimeoutCarriesTheLatestHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		denial := stubApprovalWaitSeams(t, 10*time.Millisecond, "SUDO_APPROVAL_REQUIRED")
		runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
			return denial
		}
		held := &utils.HeldRequest{ApprovalRequest: heldRequestUUID, StatusPath: heldStatusPath}
		getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
			status := "pending_approval"
			return event.EventDetails{SudoGrantStatus: &status, Held: held}, nil
		}

		err := RunExecWithApprovalWait(nil, "srv", "sudo reboot", "", "", nil, "", "", 80*time.Millisecond, io.Discard)

		var remoteErr *event.RemoteCommandError
		require.ErrorAs(t, err, &remoteErr)
		assert.Same(t, held, remoteErr.Held)
	})
}

// An approval that lands after the hold's deadline cannot be redeemed: the
// re-run is denied again, and the warning says a new approval is needed.
func TestRunExecWithApprovalWait_LateApprovalSaysANewApprovalIsNeeded(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}
	past := time.Date(2001, 11, 6, 17, 20, 0, 0, time.UTC)
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		if polls == 1 {
			status := "pending_approval"
			return event.EventDetails{SudoGrantStatus: &status, Held: &utils.HeldRequest{ApprovalRequest: heldRequestUUID, ExpiresAt: &past, StatusPath: heldStatusPath}}, nil
		}
		status := "authorized"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}

	stderr := captureStderr(t, func() {
		_ = RunExecWithApprovalWait(nil, "srv", "sudo reboot", "", "", nil, "", "", time.Minute, io.Discard)
	})

	assert.Contains(t, stderr, "granted after its deadline (2001-11-06T17:20:00Z)")
	assert.Contains(t, stderr, "needs a new approval")
}

func TestRunExecWithApprovalWait_ApprovalWithNoDeadlineKeepsTheReusedGrantWarning(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	denial.Held = &utils.HeldRequest{ApprovalRequest: heldRequestUUID, StatusPath: heldStatusPath}
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		status := "authorized"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}

	stderr := captureStderr(t, func() {
		_ = RunExecWithApprovalWait(nil, "srv", "sudo reboot", "", "", nil, "", "", time.Minute, io.Discard)
	})

	assert.Contains(t, stderr, "the grant appears already used or expired")
	assert.NotContains(t, stderr, "deadline")
}
