package event

import (
	"fmt"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

// phaseDescriptions humanizes server-classified error_phase identifiers.
var phaseDescriptions = map[string]string{
	"agent_disconnected":              "agent never acknowledged the command (likely disconnected)",
	"agent_timeout":                   "agent acknowledged the command but did not return a result in time",
	"remote_command_exceeded_timeout": "remote command exceeded its execution timeout",
}

// RemoteCommandError is returned when the remote command completed but exited
// with a non-zero status. Callers populate ExitCode from the server response;
// RunCommand falls back to 1 when the server omits exit_code.
type RemoteCommandError struct {
	Output     string
	ExitCode   int
	ErrorPhase string
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

// DescribePhase returns the human-readable description for an error_phase,
// or the raw identifier when the phase is unknown.
func DescribePhase(phase string) string {
	if desc, ok := phaseDescriptions[phase]; ok {
		return desc
	}
	return phase
}
