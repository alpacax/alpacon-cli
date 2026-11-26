package utils

import "fmt"

// commandRunningCtx is the machine-readable context for a command that has not finished.
// CommandStatus is the server's own status, so a caller can tell queued from running.
type commandRunningCtx struct {
	CommandID     string `json:"command_id"`
	CommandStatus string `json:"command_status"`
}

// commandRunningJSON is the JSON envelope `exec logs` prints under --output json for a command
// still in progress. Status is always CommandRunningStatus, so a script tells it apart from a
// finished command with no output, which prints nothing at all.
type commandRunningJSON struct {
	OK          bool              `json:"ok"`
	Status      string            `json:"status"`
	ExitCode    int               `json:"exit_code"`
	Message     string            `json:"message"`
	CommandID   string            `json:"command_id"`
	Context     commandRunningCtx `json:"context"`
	NextActions []NextAction      `json:"next_actions,omitempty"`
}

// PrintCommandRunning reports a command that has not finished: a {"status":"running", ...}
// envelope on stdout under --output json, else the status and a check-later line on stderr.
// It never exits; the caller keeps exit 0.
func (s *IOStreams) PrintCommandRunning(commandID, status string) {
	// Both are server-supplied and reach the terminal.
	commandID = SanitizeTerminalText(commandID)
	status = SanitizeTerminalText(status)
	retry := fmt.Sprintf("alpacon exec logs %s", commandID)

	if s.IsJSON() {
		envelope := commandRunningJSON{
			OK:        true,
			Status:    CommandRunningStatus,
			ExitCode:  0,
			Message:   fmt.Sprintf("command is still running (status: %s).", status),
			CommandID: commandID,
			Context:   commandRunningCtx{CommandID: commandID, CommandStatus: status},
			NextActions: []NextAction{{
				Command:     retry,
				Description: "Read the result once the command finishes; do not re-submit it",
			}},
		}
		if err := PrintJSONValue(s.Out, envelope); err != nil {
			// Fall back to a minimal, still-parseable object so a machine consumer
			// always sees the status field.
			_, _ = fmt.Fprintf(s.Out, `{"ok":true,"status":%q}`+"\n", CommandRunningStatus)
		}
		return
	}

	_, _ = fmt.Fprintf(s.ErrOut, "command is still running (status: %s).\nRun `%s` again to check later.\n", status, retry)
}
