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
// RunCommand falls back to 1 when the server omits exit_code.
type RemoteCommandError struct {
	Output     string
	ExitCode   int
	ErrorPhase string
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
