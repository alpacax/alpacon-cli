package utils

import (
	"fmt"
	"os"
)

// pendingApprovalCtx is the machine-readable context for a pending-approval
// signal. RequestID is the approval request to track; it is omitted when the
// originating surface cannot surface one (e.g. the exec sudo denial line carries
// only the denial code, never a request id).
type pendingApprovalCtx struct {
	RequestID string `json:"request_id,omitempty"`
}

// pendingApprovalJSON is the JSON envelope printed under --output json for a
// pending-approval signal. Status is always PendingApprovalStatus, so a machine
// consumer can match on a single stable field without parsing prose.
type pendingApprovalJSON struct {
	OK          bool               `json:"ok"`
	Status      string             `json:"status"`
	ExitCode    int                `json:"exit_code"`
	Message     string             `json:"message"`
	RequestID   string             `json:"request_id,omitempty"`
	Context     pendingApprovalCtx `json:"context"`
	NextActions []string           `json:"next_actions,omitempty"`
}

// pendingApprovalNextActions lists the actionable follow-ups for a human reading
// the message. reRunHint is the surface-specific way to retry (re-run the
// command, or attach the session); it leads, then the generic console pointer.
func pendingApprovalNextActions(reRunHint string) []string {
	actions := make([]string, 0, 2)
	if reRunHint != "" {
		actions = append(actions, reRunHint)
	}
	return append(actions, "Approve it out of band in the Alpacon console (web/Slack). Where supported, pass --wait on the original command to block until approval.")
}

// PrintPendingApproval emits the structured "pending approval" feedback for an
// action that requires out-of-band human approval and was not waited on. Under
// --output json it writes a {"status":"pending_approval", ...} envelope to
// stdout; otherwise it writes an actionable message to stderr. requestID may be
// empty when the surface cannot supply one. reRunHint is a surface-specific
// retry instruction (e.g. the exact command to re-run). It never exits — the
// caller owns process exit so the exit-code contract stays in one place.
func PrintPendingApproval(message, requestID, reRunHint string) {
	if OutputFormat == OutputFormatJSON {
		envelope := pendingApprovalJSON{
			OK:          false,
			Status:      PendingApprovalStatus,
			ExitCode:    ExitCodePendingApproval,
			Message:     message,
			RequestID:   requestID,
			Context:     pendingApprovalCtx{RequestID: requestID},
			NextActions: pendingApprovalNextActions(reRunHint),
		}
		if err := PrintJSONValue(os.Stdout, envelope); err != nil {
			// Fall back to a minimal, still-parseable object so a machine consumer
			// always sees the status field.
			_, _ = fmt.Fprintf(os.Stdout, `{"ok":false,"status":%q}`+"\n", PendingApprovalStatus)
		}
		return
	}

	CliWarning("%s", message)
	for _, action := range pendingApprovalNextActions(reRunHint) {
		fmt.Fprintf(os.Stderr, "  %s\n", action)
	}
}

// rejectedJSON is the JSON envelope printed under --output json when a
// reviewer rejected a command. Status is always RejectedStatus, so a machine
// consumer can match on a single stable field without parsing prose. Unlike
// pendingApprovalJSON it omits request_id/context: a rejected command is
// already terminal, so there is no pending request left to track.
type rejectedJSON struct {
	OK          bool     `json:"ok"`
	Status      string   `json:"status"`
	ExitCode    int      `json:"exit_code"`
	ErrorCode   string   `json:"error_code"`
	Message     string   `json:"message"`
	NextActions []string `json:"next_actions,omitempty"`
}

// rejectedNextActions lists the actionable follow-up for a human reading the
// message. Unlike pendingApprovalNextActions there is no re-run hint: a
// rejected command must not be retried.
func rejectedNextActions() []string {
	return []string{"Submit a new command from the Alpacon console (web/Slack) if the action is still needed."}
}

// PrintRejected emits the structured "rejected" feedback for a command a
// reviewer denied out of band. Under --output json it writes a
// {"status":"rejected", ...} envelope to stdout; otherwise it writes an
// actionable message to stderr. It never exits—the caller owns process exit
// so the exit-code contract stays in one place.
func PrintRejected(message string) {
	if OutputFormat == OutputFormatJSON {
		envelope := rejectedJSON{
			OK:          false,
			Status:      RejectedStatus,
			ExitCode:    ExitCodeCommandRejected,
			ErrorCode:   RejectedErrorCode,
			Message:     message,
			NextActions: rejectedNextActions(),
		}
		if err := PrintJSONValue(os.Stdout, envelope); err != nil {
			// Fall back to a minimal, still-parseable object so a machine consumer
			// always sees the status field.
			_, _ = fmt.Fprintf(os.Stdout, `{"ok":false,"status":%q}`+"\n", RejectedStatus)
		}
		return
	}

	fmt.Fprintf(os.Stderr, "%s: %s\n", Red("Error"), message)
	for _, action := range rejectedNextActions() {
		fmt.Fprintf(os.Stderr, "  %s\n", action)
	}
}
