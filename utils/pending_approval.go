package utils

import (
	"fmt"
	"os"
)

// pendingApprovalCtx is the machine-readable context for a pending-approval signal. RequestID is
// omitted when the surface has none, as an exec sudo denial carries only its code.
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
	NextActions []NextAction       `json:"next_actions,omitempty"`
}

// pendingApprovalNextActions lists the actionable follow-ups for a consumer
// reading the message. retry is the surface-specific way to retry (re-run the
// command, or attach the session); it leads, then the generic console pointer,
// then any extra actions the surface has. A zero-value retry is skipped.
func pendingApprovalNextActions(retry NextAction, extra ...NextAction) []NextAction {
	actions := make([]NextAction, 0, 2+len(extra))
	if retry != (NextAction{}) {
		actions = append(actions, retry)
	}
	actions = append(actions, NextAction{Description: "Approve it out of band in the Alpacon console (web/Slack). Where supported, pass --wait (or --wait-approval DURATION for a longer timeout) on the original command to block until approval."})
	return append(actions, extra...)
}

// PrintPendingApproval reports an action that awaits out-of-band human approval and was not
// waited on: a {"status":"pending_approval", ...} envelope on stdout under --output json, else an
// actionable message on stderr. requestID may be empty. retry leads the next actions and keeps its
// Command a pure executable string, any hint in Description; extra follows the console pointer.
// It never exits, so the exit-code contract stays with the caller.
func PrintPendingApproval(message, requestID string, retry NextAction, extra ...NextAction) {
	if OutputFormat == OutputFormatJSON {
		envelope := pendingApprovalJSON{
			OK:          false,
			Status:      PendingApprovalStatus,
			ExitCode:    ExitCodePendingApproval,
			Message:     message,
			RequestID:   requestID,
			Context:     pendingApprovalCtx{RequestID: requestID},
			NextActions: pendingApprovalNextActions(retry, extra...),
		}
		if err := PrintJSONValue(os.Stdout, envelope); err != nil {
			// Fall back to a minimal, still-parseable object so a machine consumer
			// always sees the status field.
			_, _ = fmt.Fprintf(os.Stdout, `{"ok":false,"status":%q}`+"\n", PendingApprovalStatus)
		}
		return
	}

	CliWarning("%s", message)
	for _, action := range pendingApprovalNextActions(retry, extra...) {
		fmt.Fprintf(os.Stderr, "  %s\n", action.PlainText())
	}
}
