package utils

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintPendingApproval_JSONOutput(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = captureStdout(t, func() {
			PrintPendingApproval("needs approval", "apr-123", NextAction{Command: "alpacon exec srv -- sudo reboot"})
		})
	})

	var got struct {
		OK        bool   `json:"ok"`
		Status    string `json:"status"`
		ExitCode  int    `json:"exit_code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Context   struct {
			RequestID string `json:"request_id"`
		} `json:"context"`
		NextActions []NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), "output: %s", out)

	assert.False(t, got.OK)
	assert.Equal(t, PendingApprovalStatus, got.Status, "status must be the stable machine-readable string")
	assert.Equal(t, ExitCodePendingApproval, got.ExitCode)
	assert.Equal(t, "apr-123", got.RequestID)
	assert.Equal(t, "apr-123", got.Context.RequestID)
	assert.Equal(t, "needs approval", got.Message)
	require.NotEmpty(t, got.NextActions)
	// The re-run hint leads the next actions as a pure, execable command.
	assert.Equal(t, "alpacon exec srv -- sudo reboot", got.NextActions[0].Command)
	// The console-approval pointer is guidance only—no runnable command.
	last := got.NextActions[len(got.NextActions)-1]
	assert.Empty(t, last.Command, "the console-approval pointer carries no command")
	assert.Contains(t, last.Description, "Alpacon console")
}

func TestPrintPendingApproval_JSONOutput_OmitsEmptyCommand(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = captureStdout(t, func() {
			PrintPendingApproval("needs approval", "apr-123", NextAction{Command: "alpacon exec srv -- sudo reboot"})
		})
	})

	// command has omitempty, so the description-only console pointer must not
	// serialize an empty "command" field. Only the leading re-run hint carries one.
	assert.Equal(t, 1, strings.Count(out, `"command"`), "empty command must be omitted, not serialized as \"\"\noutput: %s", out)
}

func TestPrintPendingApproval_JSONOutput_OmitsEmptyRequestID(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = captureStdout(t, func() {
			PrintPendingApproval("needs approval", "", NextAction{})
		})
	})

	// request_id has omitempty, so an absent id must not appear as an empty string.
	assert.NotContains(t, out, `"request_id"`, "empty request_id must be omitted, not serialized as \"\"")
	// status stays present and stable even without a request id.
	assert.Contains(t, out, `"status": "`+PendingApprovalStatus+`"`)
}

func TestPrintPendingApproval_JSONOutput_OmitsEmptyRetry(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = captureStdout(t, func() {
			PrintPendingApproval("needs approval", "", NextAction{})
		})
	})

	var got struct {
		NextActions []NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), "output: %s", out)
	// An empty retry hint must not add a blank leading action; only the console
	// pointer remains.
	require.Len(t, got.NextActions, 1)
	assert.Empty(t, got.NextActions[0].Command)
	assert.Contains(t, got.NextActions[0].Description, "Alpacon console")
}
