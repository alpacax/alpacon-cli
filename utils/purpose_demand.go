package utils

import (
	"fmt"
	"os"
)

// purposeRequiredCtx is the machine-readable context for a purpose demand. The
// command id is what the answer is addressed to, and the deadline is why the
// answer cannot wait for a human to read the message.
type purposeRequiredCtx struct {
	CommandID       string `json:"command_id"`
	DeadlineSeconds int    `json:"deadline_seconds"`
}

// purposeRequiredJSON is the JSON envelope printed under --output json when the
// verification gate parks a command and asks what it is for. Status is always
// PurposeRequiredStatus, so a machine consumer matches one stable field.
//
// The flags are the inverse of the pending-approval envelope on purpose:
// RequiresHumanApproval is false and AnswerableByCaller is true, because no
// approval request exists while the demand is open. A consumer that reads this
// as "wait for a human" strands a command nobody was asked about.
type purposeRequiredJSON struct {
	OK                    bool               `json:"ok"`
	Status                string             `json:"status"`
	ExitCode              int                `json:"exit_code"`
	Message               string             `json:"message"`
	CommandID             string             `json:"command_id"`
	RequiresHumanApproval bool               `json:"requires_human_approval"`
	AnswerableByCaller    bool               `json:"answerable_by_caller"`
	Guidance              string             `json:"guidance"`
	Context               purposeRequiredCtx `json:"context"`
	NextActions           []NextAction       `json:"next_actions,omitempty"`
}

// PurposeDeadlineSeconds mirrors the server's default COMMAND_PURPOSE_DEADLINE.
// Reported so a consumer knows the answer is urgent; the server owns the real
// clock, so this is copy and never a timer the CLI enforces.
const PurposeDeadlineSeconds = 60

// purposeGuidance says what makes a purpose worth stating. It travels in the
// response rather than living only in help text: an agent reads the answer to
// its own call far more reliably than documentation that context compaction may
// have dropped.
const purposeGuidance = "State a fact local to this host that the work session's description does not already imply—clock skew against a certificate's notBefore window, a duplicate config block overriding the edited value, contention for a single JVM attach slot. General knowledge adds nothing: the assessor already has it. A purpose cannot lower a command's intrinsic risk, cannot outrank the session description, and cannot make an unmeasurable command measurable; an attempt to argue the verdict down is reported and denied."

// purposeNextActions lists the follow-ups for a consumer reading the message.
// Answering leads; checking the outcome afterwards is the fallback for a demand
// that has already expired by the time anyone reads this.
func purposeNextActions(commandID string) []NextAction {
	return []NextAction{
		{
			Command:     fmt.Sprintf("alpacon exec purpose %s 'WHAT THIS COMMAND IS FOR'", commandID),
			Description: "Answer now—the demand expires in about a minute and there is one per command",
		},
		{
			Command:     fmt.Sprintf("alpacon exec logs %s", commandID),
			Description: "Read the outcome if the demand already expired; do not re-submit the command",
		},
	}
}

// PrintPurposeDemand emits the structured "state the purpose" feedback for a
// command the gate parked. Under --output json it writes a
// {"status":"purpose_required", ...} envelope to stdout; otherwise it writes an
// actionable message to stderr.
// It never exits—the caller owns process exit so the exit-code contract stays in
// one place.
func PrintPurposeDemand(message, commandID string) {
	if OutputFormat == OutputFormatJSON {
		envelope := purposeRequiredJSON{
			OK:                    false,
			Status:                PurposeRequiredStatus,
			ExitCode:              ExitCodePurposeRequired,
			Message:               message,
			CommandID:             commandID,
			RequiresHumanApproval: false,
			AnswerableByCaller:    true,
			Guidance:              purposeGuidance,
			Context:               purposeRequiredCtx{CommandID: commandID, DeadlineSeconds: PurposeDeadlineSeconds},
			NextActions:           purposeNextActions(commandID),
		}
		if err := PrintJSONValue(os.Stdout, envelope); err != nil {
			// Fall back to a minimal, still-parseable object so a machine consumer
			// always sees the status field.
			_, _ = fmt.Fprintf(os.Stdout, `{"ok":false,"status":%q}`+"\n", PurposeRequiredStatus)
		}
		return
	}

	CliWarning("%s", message)
	fmt.Fprintf(os.Stderr, "  %s\n", purposeGuidance)
	for _, action := range purposeNextActions(commandID) {
		fmt.Fprintf(os.Stderr, "  %s\n", action.PlainText())
	}
}
