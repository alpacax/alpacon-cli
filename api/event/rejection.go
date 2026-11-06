package event

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/utils"
)

// CommandRejectedError is a type so the CLI can exit with ExitCode: a retry is
// refused again or files another approval request.
type CommandRejectedError struct {
	CommandID string
	Expired   bool
	// Cause is the server's rejection_cause, such as approval_rejected.
	Cause string
	// Gate is the server's rejection_gate; null for the account and signature causes, so empty.
	Gate string
	// Reported says the detail sent either key; Cause and Gate are empty without it.
	Reported bool
	// SudoGrant marks an outcome read from the sudo grant this command raised; with
	// no cause and not Expired, a reviewer decided it.
	SudoGrant bool
	// Note is the reason the approver wrote, empty when they gave none.
	Note string
}

// RejectionCauseUseFileExecution is the rejection_cause of a command line that
// carries a script body; it covers one command on one server.
const RejectionCauseUseFileExecution = "use_file_execution"

const (
	RejectionCauseApprovalExpired  = "approval_expired"
	RejectionCauseApprovalRejected = "approval_rejected"
	// RejectionCauseSignatureInvalid comes with status completed, since the agent
	// refuses the file after delivery, and IsRejected still counts it a rejection.
	RejectionCauseSignatureInvalid = "signature_invalid"
)

// rejectionText is what the CLI says about one cause or gate: what happened and
// one step, never to submit the same command again.
type rejectionText struct {
	what string
	next utils.NextAction
}

const workSessionCurrent = "alpacon work-session current"

var causeTexts = map[string]rejectionText{
	"command_acl_denied":            {"the command ACL did not admit it", utils.NextAction{Description: "The command needs a pre-approved command entry in this workspace; without one it is refused the same way."}},
	"command_acl_unavailable":       {"the command ACL's verdict could not be obtained", utils.NextAction{Description: "The command pre-approval ledger gave no verdict; the command did not run."}},
	"risk_denied":                   {"the risk assessor refused it", utils.NextAction{Description: "The workspace risk policy refuses this command; the same command is refused the same way."}},
	"risk_unavailable":              {"the risk assessor was unavailable", utils.NextAction{Description: "The risk assessor gave no verdict; the command did not run."}},
	"approval_policy_denied":        {"an approval policy refused it, or no one but the requester could decide it", utils.NextAction{Description: "The approval policy needs an approver other than the requester, or refuses the command outright."}},
	RejectionCauseApprovalRejected:  {"an approver rejected it", utils.NextAction{Description: "Ask the approver why; the decision is in the Alpacon console (web)."}},
	RejectionCauseApprovalExpired:   {"its approval request expired with no decision", utils.NextAction{Description: "Ask an approver why the request went undecided."}},
	"approval_cancelled":            {"the requester cancelled its approval request", utils.NextAction{Description: "Ask the requester why the approval request was withdrawn."}},
	"work_session_terminated":       {"its work session ended while it was held", utils.NextAction{Command: workSessionCurrent, Description: "the work session it was held under has ended; this shows the one in use now"}},
	"account_bound_to_other_person": {"the account it would run as was linked to another person before it was delivered", utils.NextAction{Description: "Only the person the account is linked to can run commands as it."}},
	"unsigned_file":                 {"the file command reached the agent without a signature", utils.NextAction{Description: "The file needs a signature; the agent runs only signed files."}},
	RejectionCauseSignatureInvalid:  {"the agent could not verify the file's signature", utils.NextAction{Description: "The file's signature has to verify; the agent refuses one that does not."}},
}

// gateTexts answers a cause this CLI does not know yet from the gate that
// refused the command.
var gateTexts = map[string]rejectionText{
	"command_acl":  {"the command ACL refused it", utils.NextAction{Description: "The command needs a pre-approved command entry in this workspace."}},
	"risk":         {"the risk assessor refused it", utils.NextAction{Description: "The workspace risk policy refuses this command."}},
	"approval":     {"the approval step refused it", utils.NextAction{Description: "Check the approval request in the Alpacon console (web)."}},
	"work_session": {"its work session refused it", utils.NextAction{Command: workSessionCurrent, Description: "shows the work session in use now"}},
}

var (
	unknownCauseText = rejectionText{"the server refused it", utils.NextAction{Description: "This CLI does not know the cause yet; the command did not run."}}
	notRecordedText  = rejectionText{"the server did not record why", utils.NextAction{Description: "Check the command in the Alpacon console (web); the cause was not recorded."}}
)

// CommandRejection carries the server's two rejection fields, each null when the
// command was not rejected and absent on an older server.
type CommandRejection struct {
	RejectionCause types.SentString `json:"rejection_cause,omitzero"`
	RejectionGate  types.SentString `json:"rejection_gate,omitzero"`
}

// IsRejected says the command was refused before it ran: the rejected status, or
// a completed command the agent refused for its signature.
func (d EventDetails) IsRejected() bool {
	return IsRejectedStatus(d.Status) || (IsCompletedStatus(d.Status) && d.RejectionCause.Value == RejectionCauseSignatureInvalid)
}

// Reported says either key was sent, which an older server never does.
func (r CommandRejection) Reported() bool {
	return r.RejectionCause.Sent || r.RejectionGate.Sent
}

// Label is the short cell a list row shows in place of its status.
func (r CommandRejection) Label() string {
	if r.RejectionCause.Value == "" {
		return "rejected: cause not recorded"
	}
	label := "rejected: " + utils.SanitizeTerminalText(r.RejectionCause.Value)
	if r.RejectionGate.Value != "" {
		label += ", gate " + utils.SanitizeTerminalText(r.RejectionGate.Value)
	}
	return label
}

// ExitCode is ExitCodeNotApproved, except a signature refusal keeps exit 1, which
// scripts already branch on for a completed command that failed.
func (e *CommandRejectedError) ExitCode() int {
	if e.Cause == RejectionCauseSignatureInvalid {
		return utils.ExitCodeGeneralError
	}
	return utils.ExitCodeNotApproved
}

// Rejection is the error for a rejected command detail.
func (d EventDetails) Rejection() *CommandRejectedError {
	e := &CommandRejectedError{
		CommandID: d.ID,
		Cause:     d.RejectionCause.Value,
		Gate:      d.RejectionGate.Value,
		Reported:  d.Reported(),
	}
	if d.RejectionNote != nil {
		e.Note = *d.RejectionNote
	}
	return e
}

func (e *CommandRejectedError) Error() string {
	switch {
	case e.Cause == RejectionCauseUseFileExecution:
		return e.subject() + " was refused because its command line carries a script body (" + RejectionCauseUseFileExecution + ")"
	case e.Cause == "" && e.Expired:
		return e.subject() + " was not approved before the approval request expired"
	case e.Cause == "" && e.SudoGrant:
		return e.subject() + " was rejected by a reviewer"
	}
	return e.detailError(e.subject())
}

func (e *CommandRejectedError) subject() string {
	if e.CommandID == "" {
		return "command"
	}
	return "command " + utils.SanitizeTerminalText(e.CommandID)
}

// ErrorCode is the rejection cause, so a caller branches on it without parsing
// the message; approval_expired for a lapsed grant with no cause, else "".
func (e *CommandRejectedError) ErrorCode() string {
	if e.Cause != "" {
		return e.Cause
	}
	if e.Expired {
		return RejectionCauseApprovalExpired
	}
	return ""
}

// ErrorSource completes the coded error the envelope reads; a rejection names
// no source.
func (*CommandRejectedError) ErrorSource() string { return "" }

func (e *CommandRejectedError) text() rejectionText {
	if e.Cause == "" {
		return notRecordedText
	}
	if t, ok := causeTexts[e.Cause]; ok {
		return t
	}
	if t, ok := gateTexts[e.Gate]; ok {
		return t
	}
	return unknownCauseText
}

func (e *CommandRejectedError) detailError(subject string) string {
	if e.Cause == "" {
		return subject + " was rejected; " + notRecordedText.what
	}
	detail := "rejection_cause: " + utils.SanitizeTerminalText(e.Cause)
	if e.Gate != "" {
		detail += ", rejection_gate: " + utils.SanitizeTerminalText(e.Gate)
	}
	return fmt.Sprintf("%s was rejected: %s (%s)", subject, e.text().what, detail)
}

// NextActions is the one step for a rejected command detail, or nil for a sudo
// grant settled with no cause and for use_file_execution, which has its own guidance.
func (e *CommandRejectedError) NextActions() []utils.NextAction {
	if e.Cause == RejectionCauseUseFileExecution || (e.Cause == "" && (e.SudoGrant || e.Expired)) {
		return nil
	}
	if note := utils.SanitizeTerminalOneLine(e.Note); e.Cause == RejectionCauseApprovalRejected && note != "" {
		return []utils.NextAction{{Description: `The approver wrote: "` + note + `"`}}
	}
	return []utils.NextAction{e.text().next}
}

// ErrorFields is the envelope's server object: both keys, null where the server
// sent null; nil for an older server.
func (e *CommandRejectedError) ErrorFields() map[string]any {
	if !e.Reported {
		return nil
	}
	fields := map[string]any{
		"rejection_cause": nullIfEmpty(e.Cause),
		"rejection_gate":  nullIfEmpty(e.Gate),
	}
	if e.Note != "" {
		fields["rejection_note"] = e.Note
	}
	return fields
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
