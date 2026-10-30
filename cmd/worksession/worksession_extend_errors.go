package worksession

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/utils"
)

// extendErrorMessage maps a server refusal from the extend action to actionable text. The coded
// refusals below carry no human detail, so the operator would otherwise see only the generic
// "request failed (code: ...)" fallback.
func extendErrorMessage(id string, err error) string {
	// The server's hint is the guidance when the refusal carries one; the
	// error output prints it under this line.
	if utils.RefusalHint(err) != "" {
		return fmt.Sprintf("Failed to extend work session: %s.", err)
	}
	code, _ := utils.ParseErrorResponse(err)
	switch code {
	case utils.WorkSessionExtensionReasonRequired:
		return "Pass --reason \"...\" describing why you need more time, and retry."
	case utils.WorkSessionExtensionAlreadyPending:
		return fmt.Sprintf("Work session %s already has an extension request pending approval; wait for it to be decided before asking again. Find its id with 'alpacon work-session describe %s --output json' (field pending_extension_request.id).", id, id)
	case utils.WorkSessionAdmissionDenied:
		return "This workspace refuses this extension outright; ask an admin to adjust the work-session admission policy."
	default:
		return fmt.Sprintf("Failed to extend work session: %s.", err)
	}
}
