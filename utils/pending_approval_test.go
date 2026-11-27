package utils

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintPendingApproval_JSONOutput(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() {
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
	assert.Equal(t, "alpacon exec srv -- sudo reboot", got.NextActions[0].Command)
	last := got.NextActions[len(got.NextActions)-1]
	assert.Empty(t, last.Command, "the console-approval pointer carries no command")
	assert.Contains(t, last.Description, "Alpacon console")
}

func TestPrintPendingApproval_JSONOutput_OmitsEmptyCommand(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() {
			PrintPendingApproval("needs approval", "apr-123", NextAction{Command: "alpacon exec srv -- sudo reboot"})
		})
	})

	assert.Equal(t, 1, strings.Count(out, `"command"`), "empty command must be omitted, not serialized as \"\"\noutput: %s", out)
}

func TestPrintPendingApproval_JSONOutput_OmitsEmptyRequestID(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() {
			PrintPendingApproval("needs approval", "", NextAction{})
		})
	})

	assert.NotContains(t, out, `"request_id"`, "empty request_id must be omitted, not serialized as \"\"")
	assert.Contains(t, out, `"status": "`+PendingApprovalStatus+`"`)
}

func TestPrintPendingApproval_JSONOutput_OmitsEmptyRetry(t *testing.T) {
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() {
			PrintPendingApproval("needs approval", "", NextAction{})
		})
	})

	var got struct {
		NextActions []NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), "output: %s", out)
	require.Len(t, got.NextActions, 1)
	assert.Empty(t, got.NextActions[0].Command)
	assert.Contains(t, got.NextActions[0].Description, "Alpacon console")
}

// The extra follow-ups a caller passes, such as a refusal's hint, ride in the
// pending envelope's next_actions after the retry.
func TestPrintPendingApproval_CarriesARefusalHint(t *testing.T) {
	const hint = "Too many requests were made in a short time; wait, then retry."
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() {
			PrintPendingApproval("Work session ws-1: timed out. The outcome is still open.", "",
				NextAction{Command: "alpacon work-session use ws-1"},
				RefusalNextActions(&hintedError{hint: hint})...)
		})
	})
	var got struct {
		NextActions []NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), "stdout: %s", out)
	assert.Contains(t, got.NextActions, NextAction{Description: hint})
}

func TestPrintPendingApprovalAwaiting_LeadsWithTheWaitAndDropsTheRerunAdvice(t *testing.T) {
	t.Parallel()
	wait := NextAction{Command: "alpacon event wait --type work_session --target s-1 --timeout 10m"}
	retry := NextAction{Command: "alpacon work-session use s-1", Description: "after approval"}
	extra := NextAction{Command: "alpacon work-session update s-1 --server <server,...>"}
	for _, format := range []string{OutputFormatJSON, OutputFormatTable} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			ios, out, errOut := TestIOStreams()
			require.NoError(t, ios.SetOutputFormat(format))

			ios.PrintPendingApprovalAwaiting("needs approval", "apr-1", wait, retry, extra)

			want := []NextAction{wait, retry, {Description: "Approve it out of band in the Alpacon console (web/Slack)."}, extra}
			if format == OutputFormatJSON {
				var got struct {
					NextActions []NextAction `json:"next_actions"`
				}
				require.NoError(t, json.Unmarshal(out.Bytes(), &got), "output: %s", out)
				assert.Equal(t, want, got.NextActions)
				return
			}
			var lines strings.Builder
			for _, action := range want {
				lines.WriteString("  " + action.PlainText() + "\n")
			}
			assert.Contains(t, errOut.String(), lines.String())
		})
	}
}

func TestPrintPendingApproval_WithoutAWaitKeepsTheRerunAdvice(t *testing.T) {
	t.Parallel()
	ios, _, errOut := TestIOStreams()

	ios.PrintPendingApproval("needs approval", "apr-1", NextAction{Command: "alpacon exec srv -- sudo reboot"})

	assert.Contains(t, errOut.String(), "Where supported, pass --wait (or --wait-approval DURATION for a longer timeout) on the original command to block until approval.")
}
