package event

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/utils"
)

// phaseDescriptions humanizes server-classified error_phase identifiers.
// "client_timeout" is CLI-side only; the server does not emit it.
var phaseDescriptions = map[string]string{
	"agent_disconnected":              "agent never acknowledged the command (likely disconnected)",
	"agent_timeout":                   "agent acknowledged the command but did not return a result in time",
	"remote_command_exceeded_timeout": "remote command exceeded its execution timeout",
	"client_timeout":                  "CLI gave up waiting for the server to report a result",

	// File lane: the agent refused to run a file execution request.
	"file_hash_mismatch":    "the file changed after it was verified, so the agent refused to run it",
	"file_payload_invalid":  "the agent could not read the file execution request",
	"file_open_failed":      "the agent could not open the file on the server",
	"file_exec_unsupported": "this server's platform cannot run a file execution safely",
	"file_too_large":        "the file is larger than the agent will run",
}

// filePhasePrefix marks every error_phase the file execution lane can emit.
const filePhasePrefix = "file_"

// genericFilePhaseDescription and genericFilePhaseHint cover a file_* phase a
// newer server emits that this CLI does not know yet, so the user never sees a
// bare code.
const (
	genericFilePhaseDescription = "the agent refused to run the file"
	genericFilePhaseHint        = "Run `alpacon exec --file` again; if it keeps failing, update the CLI and contact your workspace admin."
)

// phaseHints gives the next step for a file lane phase. Other phases have no
// hint.
var phaseHints = map[string]string{
	"file_hash_mismatch":    "The file changed after it was verified. Run `alpacon exec --file` again so it is verified again, adding `--from-server` to submit the copy now on the server; a standing approval only covers the unchanged file.",
	"file_payload_invalid":  "Run `alpacon exec --file` again. If it keeps failing, update the CLI and the agent on the server.",
	"file_open_failed":      "Check that the file exists on the server, is a regular file, and is readable by the agent, then run `alpacon exec --file` again. To write it from a local copy in the same run, use `alpacon exec --upload --file PATH --file-from LOCAL_PATH SERVER`, adding `--overwrite` to replace what is at PATH.",
	"file_exec_unsupported": "File execution is not available on this server's platform. Run the command with `alpacon exec` instead.",
	"file_too_large":        "Reduce the file size, or move the logic out of the script, and run `alpacon exec --file` again.",
}

// RemoteCommandError is returned when the remote command completed but exited
// with a non-zero status. ExitCode is 1 when the server omits exit_code.
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
//
// hint is the server's hint of the refusal the last poll got when the wait
// ended on it, so it still prints. Only the hint is kept, not the refusal: its
// code must not turn a timeout into that refusal (a work-session gate, say).
type ClientTimeoutError struct {
	hint string
}

func (e *ClientTimeoutError) ErrorHint() string { return e.hint }

// PendingApprovalError is returned when the server parked a command at the
// "awaiting_approval" status: a human must approve it out of band in the Alpacon
// console before it runs. CommandID identifies the parked job so a --wait caller can
// poll it; re-submitting would create a duplicate command and approval request.
type PendingApprovalError struct {
	CommandID string
	// FileSuggestion is the server's proposal to run the held command as a
	// verified file instead; nil when the server made none.
	FileSuggestion *FileExecutionSuggestion
}

// FileExecutionSuggestion is a held command line the server recognized as a
// script on the target, restated as a verified file: the script's path, its
// interpreter and its arguments. Interpreter is set only when the command line
// named it by absolute path; a bare name such as bash leaves it null and
// arrives in InterpreterName, since only the target knows where it resolves.
type FileExecutionSuggestion struct {
	Path            string   `json:"path"`
	Interpreter     string   `json:"interpreter"`
	InterpreterName string   `json:"interpreter_name"`
	Args            []string `json:"args"`
}

// CommandRejectedError is a type so the CLI can exit ExitCodeNotApproved: an
// agent reading a generic failure code retries, filing a fresh approval request.
// Expired separates the two settled-without-a-grant outcomes: both exit
// ExitCodeNotApproved, but only a reviewer's refusal is answered by going back to
// the reviewer—a window that simply lapsed is answered by requesting again.
type CommandRejectedError struct {
	CommandID string
	Expired   bool
	// Cause is the server's rejection_cause, such as approval_rejected for a
	// reviewer's refusal; empty on older servers.
	Cause string
}

// RejectionCauseUseFileExecution is the rejection_cause of a command whose line
// carried a script body: a heredoc, a multi-line -c or -e body, or a decoded
// payload piped into a shell. Nothing can verify a script in that shape, so the
// server can refuse it after submission, when the workspace approval policy
// blocks script bodies, and ask for the file lane instead. The refusal is per
// command, so it covers one server even when the same script went to several.
const RejectionCauseUseFileExecution = "use_file_execution"

const (
	RejectionCauseApprovalExpired  = "approval_expired"
	RejectionCauseApprovalRejected = "approval_rejected"
)

// AwaitingPurposeError is returned when the server parked a command at the
// "awaiting_purpose" status: instead of queuing it for a human, the gate asks the
// requester what the command is for. Nobody has been notified, so the answer is the
// caller's to give, with 'alpacon exec purpose'. CommandID identifies the parked job;
// re-submitting would create a second command that may run twice.
type AwaitingPurposeError struct {
	CommandID string
	// When the demand stops being answerable, as the server computed it; nil on an
	// older server or a command never asked. No endpoint publishes the window's
	// length, so the remaining window is stated only when this is present.
	ExpiresAt *time.Time
}

type EventAttributes struct {
	Server                string           `json:"server"`
	Shell                 string           `json:"shell"`
	Command               string           `json:"command"`
	Result                string           `json:"result"`
	Status                string           `json:"status"`
	Operator              string           `json:"operator"`
	RequestedAt           string           `json:"requested_at" table:"Requested At"`
	ApprovedFileExecution *json.RawMessage `json:"approved_file_execution,omitempty" table:"-"`
}

type EventDetails struct {
	ID                    string              `json:"id"`
	Shell                 string              `json:"shell"`
	Line                  string              `json:"line"`
	Success               *bool               `json:"success"`
	ExitCode              *int                `json:"exit_code"`
	ErrorPhase            *string             `json:"error_phase"`
	Result                string              `json:"result"`
	Status                string              `json:"status"`
	Cancellable           bool                `json:"cancellable"`
	ResponseDelay         float64             `json:"response_delay"`
	ElapsedTime           float64             `json:"elapsed_time"`
	AddedAt               time.Time           `json:"added_at"`
	Server                types.ServerSummary `json:"server"`
	RequestedBy           types.UserSummary   `json:"requested_by"`
	ApprovedFileExecution *json.RawMessage    `json:"approved_file_execution,omitempty"`
	// What the requester said this command is for, and when the ask expires. Both are
	// absent on an older server and on any command nobody was asked, so nil and empty
	// are ordinary here. The expiry is read rather than purpose_requested_at because no
	// endpoint publishes the window's length; purpose_requested_at is deliberately not
	// parsed: nothing shows it.
	Purpose          string     `json:"purpose"`
	PurposeExpiresAt *time.Time `json:"purpose_expires_at"`
	// The server fills these only on the command detail; the list response omits
	// them, and this struct decodes both. Pointers so an absent field stays
	// distinguishable from a status the server deliberately left empty.
	SudoApprovalRequestID *string `json:"sudo_approval_request_id"`
	SudoGrantStatus       *string `json:"sudo_grant_status"`
	// FileExecutionSuggestion is set on a held command line the server reads as
	// a script on the target; absent otherwise and on older servers.
	FileExecutionSuggestion *FileExecutionSuggestion `json:"file_execution_suggestion"`
	// RejectionCause says why a rejected command was refused; empty on older servers.
	RejectionCause string `json:"rejection_cause"`
	// The account the command ran as and the work session it ran under, so a
	// re-run printed from the detail repeats them. WorkSession is null when the
	// command ran under none.
	Username    string              `json:"username"`
	Groupname   string              `json:"groupname"`
	WorkSession *CommandWorkSession `json:"work_session"`
}

// CommandWorkSession is the work session a command detail names.
type CommandWorkSession struct {
	ID string `json:"id"`
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
	// What this one command is for. Sent only when the caller supplied it;
	// with it in hand the assessor judges on the first pass and the demand
	// round trip never happens.
	Purpose string `json:"purpose,omitempty"`
	// Declares that this client answers a purpose demand; the gate does not arm
	// without it. Unconditional because every exec path surfaces the demand to its
	// caller instead of stalling on it.
	PurposeDemandSupported bool `json:"purpose_demand_supported,omitempty"`
	// File selects the verified file lane. Its presence is what the server
	// switches on, so it is omitted from every generic-lane request and no
	// existing request changes shape. When set, MarshalJSON leaves shell,
	// line, env and data out of the body entirely—see fileLaneRequest.
	File *FileExecution `json:"file,omitempty"`
}

// FileExecution is what a file-lane submission runs: the file at Path on the
// target host, through Interpreter, with Args. Content is the bytes the
// reviewer and the assessor judge and the server hashes; the agent hashes the
// file on the target's disk and runs it only when the two digests match, so
// Content is sent byte-for-byte—never trimmed, re-encoded, or normalized.
// Content itself never travels to the agent.
type FileExecution struct {
	Path        string   `json:"path"`
	Interpreter string   `json:"interpreter"`
	Args        []string `json:"args"`
	Content     string   `json:"content"`
	// ReuseDays proposes how long an opted-in grant lasts, in days; 0 leaves
	// the key out, the shape a server older than the field accepts.
	ReuseDays int `json:"reuse_days,omitempty"`
}

// fileLaneRequest is the wire shape of a file-lane submission: CommandRequest
// minus shell, line, env and data. The server refuses a file-lane body that
// carries line, data or env by key presence—an empty string counts—so those
// fields cannot simply be omitempty: the generic lane sends them as it always
// has, and this struct is what keeps them out of the other lane.
type fileLaneRequest struct {
	Username               string         `json:"username"`
	Groupname              string         `json:"groupname"`
	ScheduledAt            *time.Time     `json:"scheduled_at"`
	Server                 string         `json:"server"`
	RunAfter               []string       `json:"run_after"`
	WorkSession            string         `json:"work_session,omitempty"`
	Purpose                string         `json:"purpose,omitempty"`
	PurposeDemandSupported bool           `json:"purpose_demand_supported,omitempty"`
	File                   *FileExecution `json:"file"`
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
	pathID      api.PathID
}

// MarshalJSON renders a generic-lane request as its fields stand and a file-lane
// request as fileLaneRequest, so the keys the server refuses on that lane are
// absent rather than empty.
func (r CommandRequest) MarshalJSON() ([]byte, error) {
	if r.File == nil {
		// A local alias type carries the fields but not this method, so the
		// default encoding applies instead of recursing.
		type generic CommandRequest
		return json.Marshal(generic(r))
	}
	return json.Marshal(fileLaneRequest{
		Username:               r.Username,
		Groupname:              r.Groupname,
		ScheduledAt:            r.ScheduledAt,
		Server:                 r.Server,
		RunAfter:               r.RunAfter,
		WorkSession:            r.WorkSession,
		Purpose:                r.Purpose,
		PurposeDemandSupported: r.PurposeDemandSupported,
		File:                   r.File,
	})
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
	switch {
	case e.Cause == RejectionCauseUseFileExecution:
		reason = "was refused because its command line carries a script body (" + RejectionCauseUseFileExecution + ")"
	case e.Cause == RejectionCauseApprovalExpired || e.Expired:
		reason = "was not approved before the approval request expired"
	case e.Cause != "" && e.Cause != RejectionCauseApprovalRejected:
		reason = "was refused (" + utils.SanitizeTerminalText(e.Cause) + ")"
	}
	if e.CommandID == "" {
		return "command " + reason
	}
	return fmt.Sprintf("command %s %s", e.CommandID, reason)
}

// ErrorCode preserves the rejection cause for callers that distinguish expiry,
// a reviewer's decision, and other refusals without parsing the message.
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

// DescribePhase returns the human-readable description for an error_phase,
// or the raw identifier when the phase is unknown. An unknown file_* phase
// gets a generic file lane description instead of the bare code.
func DescribePhase(phase string) string {
	if desc, ok := phaseDescriptions[phase]; ok {
		return desc
	}
	if strings.HasPrefix(phase, filePhasePrefix) {
		return genericFilePhaseDescription
	}
	return phase
}

// PhaseHint returns the actionable next step for an error_phase, or "" when
// the phase has none. An unknown file_* phase gets a generic file lane hint.
func PhaseHint(phase string) string {
	if hint, ok := phaseHints[phase]; ok {
		return hint
	}
	if strings.HasPrefix(phase, filePhasePrefix) {
		return genericFilePhaseHint
	}
	return ""
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

// IsAwaitingApprovalStatus reports whether status is the server's hold for a
// command awaiting out-of-band human approval, not yet delivered to the agent.
func IsAwaitingApprovalStatus(status string) bool {
	return status == "awaiting_approval"
}

// IsAwaitingPurposeStatus reports whether status is the server's hold state for
// a command parked while the gate asks the requester what it is for.
// Unlike the approval hold this one is answerable here and expires on its own
// in about a minute, after which the command takes the ordinary path.
func IsAwaitingPurposeStatus(status string) bool {
	return status == "awaiting_purpose"
}

// IsRejectedStatus reports whether status is the server's terminal state for a
// command a reviewer or the gate refused. It never ran.
func IsRejectedStatus(status string) bool {
	return status == "rejected"
}

// IsStatusOnlyFailure reports the terminal failures whose whole outcome is a
// line about the status: the command never reached a result worth printing. The polling and
// detach paths both read it, so a new status-only terminal state goes here, not at either caller.
func IsStatusOnlyFailure(status string) bool {
	return status == "stuck" || status == "error" || status == "cancelled"
}
