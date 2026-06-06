package utils

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintPendingApproval_JSONOutput(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = captureStdout(t, func() {
			PrintPendingApproval("needs approval", "apr-123", "alpacon exec srv -- sudo reboot")
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
		NextActions []string `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), "output: %s", out)

	assert.False(t, got.OK)
	assert.Equal(t, PendingApprovalStatus, got.Status, "status must be the stable machine-readable string")
	assert.Equal(t, ExitCodePendingApproval, got.ExitCode)
	assert.Equal(t, "apr-123", got.RequestID)
	assert.Equal(t, "apr-123", got.Context.RequestID)
	assert.Equal(t, "needs approval", got.Message)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec srv -- sudo reboot", got.NextActions[0], "the re-run hint leads the next actions")
}

func TestPrintPendingApproval_JSONOutput_OmitsEmptyRequestID(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = captureStdout(t, func() {
			PrintPendingApproval("needs approval", "", "")
		})
	})

	// request_id has omitempty, so an absent id must not appear as an empty string.
	assert.NotContains(t, out, `"request_id"`, "empty request_id must be omitted, not serialized as \"\"")
	// status stays present and stable even without a request id.
	assert.Contains(t, out, `"status": "`+PendingApprovalStatus+`"`)
}
