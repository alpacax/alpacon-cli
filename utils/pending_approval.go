package utils

import (
	"fmt"
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
	Server      map[string]any     `json:"server,omitempty"`
	NextActions []NextAction       `json:"next_actions,omitempty"`
}

// pendingApprovalNextActions orders wait, retry, the console pointer, then extra, skipping a zero wait or retry.
// A wait drops the advice to re-run with --wait.
func pendingApprovalNextActions(wait, retry NextAction, sessionStatus string, triggeredPause bool, extra ...NextAction) []NextAction {
	actions := make([]NextAction, 0, 3+len(extra))
	for _, action := range []NextAction{wait, retry} {
		if action != (NextAction{}) {
			actions = append(actions, action)
		}
	}
	guidance := "Approve it out of band in the Alpacon console (web/Slack)."
	if wait == (NextAction{}) {
		guidance += " Where supported, pass --wait (or --wait-approval DURATION for a longer timeout) on the original command to block until approval."
	}
	if sessionStatus == "paused" {
		guidance = "Approve or confirm it in the Alpacon web console. An authorized person must also use Resume on the paused work session before the command can run."
	} else if sessionStatus == "" && triggeredPause {
		guidance = "Approve or confirm it in the Alpacon web console. If the work session is still paused, an authorized person must also use Resume before the command can run."
	} else if sessionStatus != "" && sessionStatus != "active" {
		guidance = "Check the command and work session in the Alpacon web console; the command cannot run unless its work session is active."
	}
	actions = append(actions, NextAction{Description: guidance})
	return append(actions, extra...)
}

// PrintPendingApproval reports an action that awaits out-of-band human approval and was not
// waited on: a {"status":"pending_approval", ...} envelope on stdout under --output json, else an
// actionable message on stderr. requestID may be empty. retry leads the next actions and keeps its
// Command a pure executable string, any hint in Description; extra follows the console pointer.
// It never exits, so the exit-code contract stays with the caller.
func PrintPendingApproval(message, requestID string, retry NextAction, extra ...NextAction) {
	Stdio.PrintPendingApproval(message, requestID, retry, extra...)
}

// PrintPendingApproval is the package-level PrintPendingApproval on s.
func (s *IOStreams) PrintPendingApproval(message, requestID string, retry NextAction, extra ...NextAction) {
	s.PrintPendingApprovalWithError(message, requestID, nil, retry, extra...)
}

// PrintPendingApprovalAwaiting is PrintPendingApproval for a request that re-running the command
// would file again: wait, the command that waits on the existing one, leads the next actions.
func (s *IOStreams) PrintPendingApprovalAwaiting(message, requestID string, wait, retry NextAction, extra ...NextAction) {
	s.printPendingApproval(message, requestID, nil, wait, retry, extra...)
}

// PrintPendingApprovalWithError preserves the cause's server fields in the pending envelope.
func (s *IOStreams) PrintPendingApprovalWithError(message, requestID string, cause error, retry NextAction, extra ...NextAction) {
	s.printPendingApproval(message, requestID, cause, NextAction{}, retry, extra...)
}

func (s *IOStreams) printPendingApproval(message, requestID string, cause error, wait, retry NextAction, extra ...NextAction) {
	fields := serverErrorFields(cause)
	sessionStatus := ""
	for err := range errorChain(cause) {
		if state, ok := err.(interface{ CurrentWorkSessionStatus() string }); ok {
			sessionStatus = state.CurrentWorkSessionStatus()
			break
		}
	}
	actions := pendingApprovalNextActions(wait, retry, sessionStatus, fields["work_session_paused"] == true, extra...)
	if s.IsJSON() {
		envelope := pendingApprovalJSON{
			OK:          false,
			Status:      PendingApprovalStatus,
			ExitCode:    ExitCodePendingApproval,
			Message:     message,
			RequestID:   requestID,
			Context:     pendingApprovalCtx{RequestID: requestID},
			Server:      fields,
			NextActions: actions,
		}
		if err := PrintJSONValue(s.Out, envelope); err != nil {
			// Fall back to a minimal, still-parseable object so a machine consumer
			// always sees the status field.
			_, _ = fmt.Fprintf(s.Out, `{"ok":false,"status":%q}`+"\n", PendingApprovalStatus)
		}
		return
	}

	s.Warning("%s", message)
	for _, action := range actions {
		fmt.Fprintf(s.ErrOut, "  %s\n", action.PlainText())
	}
}
