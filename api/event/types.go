package event

import (
	"fmt"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

// phaseDescriptions humanizes server-classified error_phase identifiers.
// "client_timeout" is CLI-side only; the server does not emit it.
var phaseDescriptions = map[string]string{
	"agent_disconnected":              "agent never acknowledged the command (likely disconnected)",
	"agent_timeout":                   "agent acknowledged the command but did not return a result in time",
	"remote_command_exceeded_timeout": "remote command exceeded its execution timeout",
	"client_timeout":                  "CLI gave up waiting for the server to report a result",
}

// RemoteCommandError is returned when the remote command completed but exited
// with a non-zero status. Callers populate ExitCode from the server response;
// errorFromDetails falls back to 1 when the server omits exit_code.
type RemoteCommandError struct {
	Output     string
	ExitCode   int
	ErrorPhase string
	// CommandID identifies the job this failure came from, so an approval wait
	// has something to poll. ApprovalRequestID is filled in by that wait, not
	// by the server response this error is built from.
	CommandID         string
	ApprovalRequestID string
}

// ClientTimeoutError is returned when the CLI gave up polling for the command
// result before the server reported a terminal status.
type ClientTimeoutError struct{}

// PendingApprovalError is returned when the server parked a command at the
// "awaiting_approval" status: the job was accepted but a human must approve it
// out of band in the Alpacon console before it runs. CommandID identifies the
// parked job so a --wait caller can poll it to completion—re-submitting would
// create a duplicate command (and a duplicate approval request).
type PendingApprovalError struct {
	CommandID string
}

// CommandRejectedError is a type so the CLI can exit ExitCodeNotApproved: an
// agent reading a generic failure code retries, filing a fresh approval request.
// Expired separates the two settled-without-a-grant outcomes: both exit
// ExitCodeNotApproved, but only a reviewer's refusal is answered by going back to
// the reviewer—a window that simply lapsed is answered by requesting again.
type CommandRejectedError struct {
	CommandID string
	Expired   bool
}

// AwaitingPurposeError is returned when the server parked a command at the
// "awaiting_purpose" status: the verification gate would have queued it for a
// human, so instead of opening an approval request it is asking the requester
// what the command is for (ADR 0052). Nobody has been notified, so this is not
// a wait—the answer is the caller's to give, with 'alpacon exec purpose'.
// CommandID identifies the parked job; re-submitting would create a second
// command that gets its own demand and may run twice.
type AwaitingPurposeError struct {
	CommandID string
	// When the demand stops being answerable, as the server computed it. Nil
	// when absent—an older server, or a command that was never asked. The
	// reporter states the remaining window only when this is present:
	// COMMAND_PURPOSE_DEADLINE is env-overridable and no endpoint publishes it,
	// so a client deriving this itself would be guessing the window's length.
	ExpiresAt *time.Time
}

type EventAttributes struct {
	Server      string `json:"server"`
	Shell       string `json:"shell"`
	Command     string `json:"command"`
	Result      string `json:"result"`
	Status      string `json:"status"`
	Operator    string `json:"operator"`
	RequestedAt string `json:"requested_at" table:"Requested At"`
}

type EventDetails struct {
	ID            string              `json:"id"`
	Shell         string              `json:"shell"`
	Line          string              `json:"line"`
	Success       *bool               `json:"success"`
	ExitCode      *int                `json:"exit_code"`
	ErrorPhase    *string             `json:"error_phase"`
	Result        string              `json:"result"`
	Status        string              `json:"status"`
	Cancellable   bool                `json:"cancellable"`
	ResponseDelay float64             `json:"response_delay"`
	ElapsedTime   float64             `json:"elapsed_time"`
	AddedAt       time.Time           `json:"added_at"`
	Server        types.ServerSummary `json:"server"`
	RequestedBy   types.UserSummary   `json:"requested_by"`
	// What the requester said this command is for, and when the ask expires
	// (ADR 0052). Both are absent on a server predating the read exposure, and
	// on any command nobody was asked—which is every command until the gate is
	// enabled, so nil and empty are the ordinary shapes here.
	//
	// The expiry is server-derived and read instead of `purpose_requested_at`:
	// the window's length is COMMAND_PURPOSE_DEADLINE and no endpoint publishes
	// it, so a start time alone would leave this client guessing the length.
	// The start time is deliberately not parsed—no surface here shows it, and a
	// field read by nothing is a promise no surface keeps.
	Purpose          string     `json:"purpose"`
	PurposeExpiresAt *time.Time `json:"purpose_expires_at"`
	// The server fills these only on the command detail; the list response omits
	// them, and this struct decodes both. Pointers so an absent field stays
	// distinguishable from a status the server deliberately left empty.
	SudoApprovalRequestID *string `json:"sudo_approval_request_id"`
	SudoGrantStatus       *string `json:"sudo_grant_status"`
}

type CommandRequest struct {
	Shell       string            `json:"shell"`
	Line        string            `json:"line"`
	Env         map[string]string `json:"env"`
	Data        string            `json:"data"`
	Username    string            `json:"username"`
	Groupname   string            `json:"groupname"`
	ScheduledAt *time.Time        `json:"scheduled_at"`
	Server      string            `json:"server"`
	RunAfter    []string          `json:"run_after"`
	WorkSession string            `json:"work_session,omitempty"`
	// What this one command is for (ADR 0052). Sent only when the caller
	// supplied it; with it in hand the assessor judges on the first pass and the
	// demand round trip never happens, which is the steady state the ADR intends.
	Purpose string `json:"purpose,omitempty"`
	// Declares that this client answers a purpose demand. The gate does not arm
	// without it, so the declaration is the opt-in—and it is unconditional here
	// because every exec path surfaces the demand to its caller instead of
	// stalling on it.
	PurposeDemandSupported bool `json:"purpose_demand_supported,omitempty"`
}

// CommandPurposeRequest is the body of the purpose-demand answer. Write-only on
// the server side: the 202 carries no echo of what was just sent.
type CommandPurposeRequest struct {
	Purpose string `json:"purpose"`
}

type CommandResponse struct {
	ID          string              `json:"id"`
	Shell       string              `json:"shell"`
	Line        string              `json:"line"`
	Data        string              `json:"data"`
	Username    string              `json:"username"`
	Groupname   string              `json:"groupname"`
	AddedAt     time.Time           `json:"added_at"`
	ScheduledAt time.Time           `json:"scheduled_at"`
	Server      types.ServerSummary `json:"server"`
	RequestedBy types.UserSummary   `json:"requested_by"`
	RunAfter    []any               `json:"run_after"`
}

func (e *RemoteCommandError) Error() string {
	if e.ErrorPhase != "" {
		return fmt.Sprintf("remote command failed (%s, exit %d)", e.ErrorPhase, e.ExitCode)
	}
	return fmt.Sprintf("remote command exited with code %d", e.ExitCode)
}

func (*ClientTimeoutError) Error() string {
	return "CLI timed out waiting for command result"
}

func (*PendingApprovalError) Error() string {
	return "command is awaiting human approval"
}

func (*AwaitingPurposeError) Error() string {
	return "command is awaiting the requester's stated purpose"
}

func (e *CommandRejectedError) Error() string {
	reason := "was rejected by a reviewer"
	if e.Expired {
		reason = "was not approved before the request expired"
	}
	if e.CommandID == "" {
		return "command " + reason
	}
	return fmt.Sprintf("command %s %s", e.CommandID, reason)
}

// DescribePhase returns the human-readable description for an error_phase,
// or the raw identifier when the phase is unknown.
func DescribePhase(phase string) string {
	if desc, ok := phaseDescriptions[phase]; ok {
		return desc
	}
	return phase
}

// IsRunningStatus reports whether status represents an in-progress (non-terminal) command.
func IsRunningStatus(status string) bool {
	switch status {
	case "queued", "scheduled", "delivered", "verifying", "running", "acked":
		return true
	default:
		return false
	}
}

// IsAwaitingApprovalStatus reports whether status is the server's hold state for a
// command parked pending out-of-band human approval (HITL). The server exposes
// this via Command.compute_status when verification_status is "awaiting_approval"
// and the command has not yet been delivered to the agent.
func IsAwaitingApprovalStatus(status string) bool {
	return status == "awaiting_approval"
}

// IsAwaitingPurposeStatus reports whether status is the server's hold state for
// a command parked while the gate asks the requester what it is for (ADR 0052).
// Unlike the approval hold this one is answerable here and expires on its own
// in about a minute, after which the command takes the ordinary path.
func IsAwaitingPurposeStatus(status string) bool {
	return status == "awaiting_purpose"
}

// IsRejectedStatus reports whether status is the server's terminal state for a
// command a reviewer refused. It never ran, and re-submitting it only files a
// second approval request.
func IsRejectedStatus(status string) bool {
	return status == "rejected"
}
