package utils

import (
	"fmt"
	"io"
)

// NewMFARequiredExitError returns the exit-9 failure for an action that waits on a person to
// finish MFA in a browser: message on a warning line and next under a "Next:" list in table mode,
// or one error envelope with error_code mfa_required and next as its next_actions under --output
// json. Nothing was refused, so no server error rides along and no footer is printed.
func NewMFARequiredExitError(operation, message string, next []NextAction) error {
	return &ExitError{
		Code:      ExitCodeMFARequired,
		operation: operation,
		errorCode: ErrorCodeMFARequired,
		msg:       "%s",
		args:      []any{message},
		print: func(w io.Writer, s *IOStreams) {
			if s.IsJSON() {
				envelope := newFailureEnvelope(ExitCodeMFARequired, ErrorCodeMFARequired, operation, nil, message)
				envelope.NextActions = next
				PrintJSONError(w, envelope)
				return
			}
			_, _ = fmt.Fprintf(w, "%s: %s\n", s.Yellow("Warning"), cliMessage("%s", message))
			if len(next) == 0 {
				return
			}
			_, _ = fmt.Fprintln(w, "Next:")
			for _, action := range next {
				_, _ = fmt.Fprintf(w, "  %s\n", action.PlainText())
			}
		},
	}
}
