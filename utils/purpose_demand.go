package utils

import (
	"fmt"
	"math"
	"os"
	"time"
)

// PurposeDemandLead is the first half of the message every surface prints for an
// open demand. `exec` and `exec logs` differ only in what they add after it, so
// keeping the shared half here stops one from being reworded without the other.
const PurposeDemandLead = "Purpose required—the verification gate held this command and is asking what it is for. " +
	"No approver has been notified: state the purpose and it is judged again, once, with that in hand. " +
	"Stay silent and the command is not blocked—it takes the ordinary path when the demand expires."

// purposeGuidance says what makes a purpose worth stating. It travels in the
// response rather than living only in help text: an agent reads the answer to
// its own call far more reliably than documentation that context compaction may
// have dropped.
const purposeGuidance = "State a fact local to this host that the work session's description does not already imply—clock skew against a certificate's notBefore window, a duplicate config block overriding the edited value, contention for a single JVM attach slot. General knowledge adds nothing: the assessor already has it. A purpose cannot lower a command's intrinsic risk, cannot outrank the session description, and cannot make an unmeasurable command measurable; an attempt to argue the verdict down is reported and denied."

// purposeRequiredCtx is the machine-readable context for a purpose demand. The
// command id is what the answer is addressed to. DeadlineSeconds is what remains
// of the window, computed from the server's own timestamp; it is omitted rather
// than guessed when the server did not supply one, because
// COMMAND_PURPOSE_DEADLINE is env-overridable and a copied default would be a
// deadline that does not exist on a workspace which raised it.
type purposeRequiredCtx struct {
	CommandID       string `json:"command_id"`
	DeadlineSeconds *int   `json:"deadline_seconds,omitempty"`
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

// remainingPurposeWindow reports the seconds left before the demand expires, or
// nil when the server sent no expiry. The expiry is server-derived because the
// window's length is COMMAND_PURPOSE_DEADLINE and no endpoint publishes it, so
// there is no length for this client to assume and nothing here can be wrong
// about one. A window already elapsed reports 0 rather than a negative: the
// answer is late either way, and a negative invites arithmetic on it.
func remainingPurposeWindow(expiresAt *time.Time) *int {
	if expiresAt == nil {
		return nil
	}
	left := int(math.Max(0, time.Until(*expiresAt).Seconds()))
	return &left
}

// purposeNextActions lists the follow-ups for a consumer reading the message.
// Answering leads; checking the outcome afterwards is the fallback for a demand
// that has already expired by the time anyone reads this.
func purposeNextActions(commandID string) []NextAction {
	return []NextAction{
		{
			Command:     fmt.Sprintf("alpacon exec purpose %s 'WHAT THIS COMMAND IS FOR'", commandID),
			Description: "Answer now—the demand expires shortly and there is one per command",
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
// actionable message to stderr. expiresAt may be nil, in which case no deadline
// is reported rather than one being invented.
// It never exits—the caller owns process exit so the exit-code contract stays in
// one place.
func PrintPurposeDemand(message, commandID string, expiresAt *time.Time) {
	// Sanitized once, here: the id is server-supplied and every line below
	// interpolates it into text written straight to the terminal (#364).
	commandID = SanitizeTerminalText(commandID)
	remaining := remainingPurposeWindow(expiresAt)

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
			Context:               purposeRequiredCtx{CommandID: commandID, DeadlineSeconds: remaining},
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
	if remaining != nil {
		fmt.Fprintf(os.Stderr, "  About %ds left to answer.\n", *remaining)
	}
	fmt.Fprintf(os.Stderr, "  %s\n", purposeGuidance)
	for _, action := range purposeNextActions(commandID) {
		fmt.Fprintf(os.Stderr, "  %s\n", action.PlainText())
	}
}

// purposeAnswerJSON is the envelope `alpacon exec purpose` prints under
// --output json. ADR 0052 is an agent-facing flow end to end: `exec` and
// `exec logs` both emit a machine-readable result, and the command that answers
// them has to as well, or an agent that branched on exit 7 and answered has the
// exit code and nothing else.
type purposeAnswerJSON struct {
	OK          bool         `json:"ok"`
	Status      string       `json:"status"`
	ExitCode    int          `json:"exit_code"`
	Message     string       `json:"message"`
	CommandID   string       `json:"command_id"`
	NextActions []NextAction `json:"next_actions,omitempty"`
}

// PrintPurposeAccepted reports a recorded purpose. There is no verdict yet—the
// command re-enters judgment on the worker—so the result names where to read
// the outcome rather than pretending to carry it.
func PrintPurposeAccepted(commandID string) {
	commandID = SanitizeTerminalText(commandID)
	next := NextAction{
		Command:     fmt.Sprintf("alpacon exec logs %s", commandID),
		Description: "Read the outcome; the command is being judged again",
	}
	message := "Purpose recorded. The command is being judged again with it in hand."

	if OutputFormat == OutputFormatJSON {
		envelope := purposeAnswerJSON{
			OK:          true,
			Status:      "purpose_recorded",
			Message:     message,
			CommandID:   commandID,
			NextActions: []NextAction{next},
		}
		if err := PrintJSONValue(os.Stdout, envelope); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, `{"ok":true,"status":"purpose_recorded"}`+"\n")
		}
		return
	}

	fmt.Fprintf(os.Stderr, "%s\n", message)
	fmt.Fprintf(os.Stderr, "  %s\n", next.PlainText())
}

// PrintPurposeRefused reports a rejected answer. The server answers a settled
// command and an answer from the wrong principal with one code, so neither this
// message nor the envelope can say which happened—hence the pointer at the
// command's own state rather than a guess, and never at a resubmission.
// It never exits; the caller owns the exit-code contract.
func PrintPurposeRefused(commandID string, cause error) {
	commandID = SanitizeTerminalText(commandID)
	message := fmt.Sprintf(
		"failed to state the purpose for %s: %s. The demand may have expired, it may already have been answered, "+
			"or this credential may not be the one that submitted the command.",
		commandID, cause,
	)
	next := NextAction{
		Command:     fmt.Sprintf("alpacon exec logs %s", commandID),
		Description: "Read the command's state; do not re-submit it, which would create a second command",
	}

	if OutputFormat == OutputFormatJSON {
		envelope := purposeAnswerJSON{
			OK:          false,
			Status:      "purpose_refused",
			ExitCode:    ExitCodeGeneralError,
			Message:     message,
			CommandID:   commandID,
			NextActions: []NextAction{next},
		}
		if err := PrintJSONValue(os.Stdout, envelope); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, `{"ok":false,"status":"purpose_refused"}`+"\n")
		}
		return
	}

	CliWarning("%s", message)
	fmt.Fprintf(os.Stderr, "  %s\n", next.PlainText())
}
