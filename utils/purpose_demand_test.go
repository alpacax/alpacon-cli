package utils

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const purposeTestCommandID = "a1b2c3d4-5678-abcd-ef01-234567890abc"

// purposeRequiredSignal mirrors every field of the published envelope, not just the ones a caller
// branches on, so dropping a field README documents fails the suite.
type purposeRequiredSignal struct {
	OK                    bool   `json:"ok"`
	Status                string `json:"status"`
	ExitCode              int    `json:"exit_code"`
	Message               string `json:"message"`
	CommandID             string `json:"command_id"`
	RequiresHumanApproval bool   `json:"requires_human_approval"`
	AnswerableByCaller    bool   `json:"answerable_by_caller"`
	Guidance              string `json:"guidance"`
	Context               struct {
		CommandID       string `json:"command_id"`
		DeadlineSeconds *int   `json:"deadline_seconds"`
	} `json:"context"`
	NextActions []NextAction `json:"next_actions"`
}

type purposeAnswerSignal struct {
	OK          bool         `json:"ok"`
	Status      string       `json:"status"`
	ExitCode    int          `json:"exit_code"`
	Message     string       `json:"message"`
	CommandID   string       `json:"command_id"`
	NextActions []NextAction `json:"next_actions"`
}

func capturePurposeJSON(t *testing.T, expiresAt *time.Time) purposeRequiredSignal {
	t.Helper()
	return capturePurposeOutput(t, func() {
		PrintPurposeDemand(PurposeDemandLead, purposeTestCommandID, expiresAt)
	})
}

// capturePurposeJSONIn prints a demand whose window ends window from now on a frozen
// clock; the pipe stays outside the bubble, which cannot block on it.
func capturePurposeJSONIn(t *testing.T, window time.Duration) purposeRequiredSignal {
	t.Helper()
	return capturePurposeOutput(t, func() {
		synctest.Test(t, func(*testing.T) {
			expires := time.Now().Add(window)
			PrintPurposeDemand(PurposeDemandLead, purposeTestCommandID, &expires)
		})
	})
}

func capturePurposeOutput(t *testing.T, emit func()) purposeRequiredSignal {
	t.Helper()
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, emit)
	})
	var got purposeRequiredSignal
	require.NoError(t, json.Unmarshal([]byte(out), &got), "stdout: %s", out)
	return got
}

func TestPrintPurposeDemand_EnvelopeCarriesTheWholeContract(t *testing.T) {
	expires := time.Now().Add(45 * time.Second)
	got := capturePurposeJSON(t, &expires)

	assert.False(t, got.OK)
	assert.Equal(t, PurposeRequiredStatus, got.Status)
	assert.Equal(t, ExitCodePurposeRequired, got.ExitCode)
	assert.Contains(t, got.Message, PurposeDemandLead)
	assert.Equal(t, purposeTestCommandID, got.CommandID)
	assert.Equal(t, purposeTestCommandID, got.Context.CommandID)

	// Inverted from the pending-approval envelope: no approval request exists
	// while the demand is open, so a consumer that waits for a human strands a
	// command nobody was asked about.
	assert.False(t, got.RequiresHumanApproval)
	assert.True(t, got.AnswerableByCaller)

	assert.Contains(t, got.Guidance, "local to this host")
	assert.Contains(t, got.Guidance, "cannot lower")

	require.Len(t, got.NextActions, 2)
	assert.Contains(t, got.NextActions[0].Command, "alpacon exec purpose "+purposeTestCommandID)
	// The fallback matters as much as the lead: a demand that already expired
	// must send the reader to the outcome, never to a resubmission.
	assert.Contains(t, got.NextActions[1].Command, "alpacon exec logs "+purposeTestCommandID)
	assert.Contains(t, got.NextActions[1].Description, "do not re-submit")
}

func TestPrintPurposeDemand_DeadlineMeasuresAgainstTheServersExpiry(t *testing.T) {
	got := capturePurposeJSONIn(t, 30*time.Second)

	require.NotNil(t, got.Context.DeadlineSeconds)
	assert.Equal(t, 30, *got.Context.DeadlineSeconds)
}

func TestPrintPurposeDemand_AnExpiredWindowReportsZeroNotNegative(t *testing.T) {
	// The answer is late either way, and a negative number invites a consumer
	// to do arithmetic with it.
	got := capturePurposeJSONIn(t, -10*time.Minute)

	require.NotNil(t, got.Context.DeadlineSeconds)
	assert.Equal(t, 0, *got.Context.DeadlineSeconds)
}

func TestPrintPurposeDemand_NoExpiryReportsNoDeadline(t *testing.T) {
	// An older server sends no expiry, and inventing one could publish a deadline that does not
	// exist: the server's deployment can change the window's length.
	got := capturePurposeJSON(t, nil)

	assert.Nil(t, got.Context.DeadlineSeconds)
}

func TestPrintPurposeDemand_TableModeIsActionableOnStderr(t *testing.T) {
	expires := time.Now().Add(45 * time.Second)
	var stdout, stderr string
	withFormat(OutputFormatTable, func() {
		stdout, stderr = testutil.CaptureOutput(t, func() {
			PrintPurposeDemand(PurposeDemandLead, purposeTestCommandID, &expires)
		})
	})

	assert.Empty(t, stdout, "stdout stays clean for the command's own output")
	assert.Contains(t, stderr, "Purpose required")
	assert.Contains(t, stderr, "the command is judged again without one")
	assert.Contains(t, stderr, "local to this host")
	assert.Contains(t, stderr, "alpacon exec purpose "+purposeTestCommandID)
	assert.Contains(t, stderr, "alpacon exec logs "+purposeTestCommandID)
}

func TestPrintPurposeDemand_SanitizesTheServerSuppliedID(t *testing.T) {
	// The id is server-supplied and every line writes it straight to the terminal.
	var stderr string
	withFormat(OutputFormatTable, func() {
		_, stderr = testutil.CaptureOutput(t, func() {
			PrintPurposeDemand(PurposeDemandLead, "cmd-\x1b[31mred\x07", nil)
		})
	})

	assert.NotContains(t, stderr, "\x1b[31m")
	assert.NotContains(t, stderr, "\x07")
}

func TestPrintPurposeAccepted_JSONNamesWhereToReadTheOutcome(t *testing.T) {
	// There is no verdict yet—the command re-enters judgment on the worker—so
	// the result must point at the outcome rather than imply it carries one.
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() {
			PrintPurposeAccepted(purposeTestCommandID)
		})
	})

	var got purposeAnswerSignal
	require.NoError(t, json.Unmarshal([]byte(out), &got), "stdout: %s", out)
	assert.True(t, got.OK)
	assert.Equal(t, "purpose_recorded", got.Status)
	assert.Equal(t, purposeTestCommandID, got.CommandID)
	require.Len(t, got.NextActions, 1)
	assert.Contains(t, got.NextActions[0].Command, "alpacon exec logs "+purposeTestCommandID)
}

func TestPrintPurposeRefused_JSONDoesNotSendTheCallerToResubmit(t *testing.T) {
	// A settled command and a bystander's answer share one server code, so the
	// envelope cannot say which happened—and must not turn that uncertainty
	// into a resubmission, which would create a second command.
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() {
			PrintPurposeRefused(purposeTestCommandID, errors.New("400 Bad Request"))
		})
	})

	var got purposeAnswerSignal
	require.NoError(t, json.Unmarshal([]byte(out), &got), "stdout: %s", out)
	assert.False(t, got.OK)
	assert.Equal(t, "purpose_refused", got.Status)
	assert.Equal(t, ExitCodeGeneralError, got.ExitCode)
	assert.Equal(t, purposeTestCommandID, got.CommandID)
	require.Len(t, got.NextActions, 1)
	assert.Contains(t, got.NextActions[0].Command, "alpacon exec logs")
	assert.Contains(t, got.NextActions[0].Description, "do not re-submit")
}

func TestPrintPurposeAnswer_TableModeKeepsStdoutClean(t *testing.T) {
	var stdout, stderr string
	withFormat(OutputFormatTable, func() {
		stdout, stderr = testutil.CaptureOutput(t, func() {
			PrintPurposeAccepted(purposeTestCommandID)
		})
	})

	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Purpose recorded")
}

func TestPrintPurposeDemand_StatesLocalDeadlineAndLapseOutcome(t *testing.T) {
	for _, format := range []string{OutputFormatTable, OutputFormatJSON} {
		for _, window := range []time.Duration{time.Minute, -time.Minute} {
			t.Run(format+" "+window.String(), func(t *testing.T) {
				var stdout, stderr, want string
				withFormat(format, func() {
					stdout, stderr = testutil.CaptureOutput(t, func() {
						synctest.Test(t, func(*testing.T) {
							expires := time.Now().Add(window).In(time.FixedZone("server", 9*60*60))
							left := "60"
							if window < 0 {
								left = "0"
							}
							want = "If no purpose arrives by " + expires.Local().Format("15:04:05") + " (about " + left + " s), the command is judged again without one: it runs, waits for a human approver (who is notified then), or is refused. Read the result with `alpacon exec logs " + purposeTestCommandID + "`; do not resubmit."
							PrintPurposeDemand(PurposeDemandLead, purposeTestCommandID, &expires)
						})
					})
				})
				if format == OutputFormatJSON {
					var got purposeRequiredSignal
					require.NoError(t, json.Unmarshal([]byte(stdout), &got))
					assert.Contains(t, got.Message, want)
				} else {
					assert.Empty(t, stdout)
					assert.Contains(t, stderr, want)
				}
				assert.NotContains(t, stdout+stderr, "ordinary path")
			})
		}
	}
}

// A refused answer whose refusal carries the server's hint leads with the
// hint in place of the local guess at why; without a hint the guess stays.
func TestPrintPurposeRefused_ServerHintReplacesTheGuess(t *testing.T) {
	const hint = "Your account may not act on this command, which it did not request."
	refusal := &hintedError{code: "event_command_not_requester", hint: hint}

	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() { PrintPurposeRefused(purposeTestCommandID, refusal) })
	})
	var got purposeAnswerSignal
	require.NoError(t, json.Unmarshal([]byte(out), &got), "stdout: %s", out)
	assert.Equal(t, "failed to state the purpose for "+purposeTestCommandID+": permission denied.", got.Message)
	require.Len(t, got.NextActions, 2)
	assert.Equal(t, NextAction{Description: hint}, got.NextActions[0])
	assert.Contains(t, got.NextActions[1].Command, "alpacon exec logs")

	var stderr string
	withFormat(OutputFormatTable, func() {
		_, stderr = testutil.CaptureOutput(t, func() { PrintPurposeRefused(purposeTestCommandID, refusal) })
	})
	assert.Equal(t, 1, strings.Count(stderr, hint), "stderr: %q", stderr)
	assert.NotContains(t, stderr, "may have expired")

	withFormat(OutputFormatTable, func() {
		_, stderr = testutil.CaptureOutput(t, func() {
			PrintPurposeRefused(purposeTestCommandID, &hintedError{code: "event_command_not_requester"})
		})
	})
	assert.Contains(t, stderr, "may have expired")
}
