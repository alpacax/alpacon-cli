package utils

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const purposeTestCommandID = "a1b2c3d4-5678-abcd-ef01-234567890abc"

// purposeRequiredSignal mirrors every field of the published envelope, not just
// the ones a caller happens to branch on. An earlier version omitted `message`
// and `context`, which meant deleting them from the envelope kept the suite
// green while breaking a contract README documents.
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

func capturePurposeJSON(t *testing.T, expiresAt *time.Time) purposeRequiredSignal {
	t.Helper()
	var out string
	withFormat(OutputFormatJSON, func() {
		out = testutil.CaptureStdout(t, func() {
			PrintPurposeDemand(PurposeDemandLead, purposeTestCommandID, expiresAt)
		})
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
	assert.Equal(t, PurposeDemandLead, got.Message)
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
	// The server derives the expiry because the window's length is a setting no
	// endpoint publishes—so this client has no length to assume and cannot be
	// wrong about one.
	expires := time.Now().Add(30 * time.Second)
	got := capturePurposeJSON(t, &expires)

	require.NotNil(t, got.Context.DeadlineSeconds)
	assert.InDelta(t, 30, *got.Context.DeadlineSeconds, 2)
}

func TestPrintPurposeDemand_AnExpiredWindowReportsZeroNotNegative(t *testing.T) {
	// The answer is late either way, and a negative number invites a consumer
	// to do arithmetic with it.
	past := time.Now().Add(-10 * time.Minute)
	got := capturePurposeJSON(t, &past)

	require.NotNil(t, got.Context.DeadlineSeconds)
	assert.Equal(t, 0, *got.Context.DeadlineSeconds)
}

func TestPrintPurposeDemand_NoExpiryReportsNoDeadline(t *testing.T) {
	// An older server sends no expiry. Inventing one would publish a deadline
	// that does not exist on a workspace which raised COMMAND_PURPOSE_DEADLINE.
	got := capturePurposeJSON(t, nil)

	assert.Nil(t, got.Context.DeadlineSeconds)
}

func TestPrintPurposeDemand_TableModeIsActionableOnStderr(t *testing.T) {
	// The default path—no --output json—had no coverage at all.
	expires := time.Now().Add(45 * time.Second)
	var stdout, stderr string
	withFormat(OutputFormatTable, func() {
		stdout, stderr = testutil.CaptureOutput(t, func() {
			PrintPurposeDemand(PurposeDemandLead, purposeTestCommandID, &expires)
		})
	})

	assert.Empty(t, stdout, "stdout stays clean for the command's own output")
	assert.Contains(t, stderr, "Purpose required")
	assert.Contains(t, stderr, "left to answer")
	assert.Contains(t, stderr, "local to this host")
	assert.Contains(t, stderr, "alpacon exec purpose "+purposeTestCommandID)
	assert.Contains(t, stderr, "alpacon exec logs "+purposeTestCommandID)
}

func TestPrintPurposeDemand_SanitizesTheServerSuppliedID(t *testing.T) {
	// The id is server-supplied and every line interpolates it into text
	// written straight to the terminal (#364).
	var stderr string
	withFormat(OutputFormatTable, func() {
		_, stderr = testutil.CaptureOutput(t, func() {
			PrintPurposeDemand(PurposeDemandLead, "cmd-\x1b[31mred\x07", nil)
		})
	})

	assert.NotContains(t, stderr, "\x1b[31m")
	assert.NotContains(t, stderr, "\x07")
}
