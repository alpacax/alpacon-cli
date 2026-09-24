package worksession

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/utils"
)

// extendErrorMessage maps a server refusal from the extend action to actionable
// text. The three coded refusals below carry no human detail of their own (see
// utils.ParseErrorResponse), so without this the operator would see only the
// generic "request failed (code: ...)" fallback and not what to do about it.
func extendErrorMessage(id string, err error) string {
	code, _ := utils.ParseErrorResponse(err)
	switch code {
	case utils.WorkSessionExtensionReasonRequired:
		return "This workspace requires approval for this request; pass --reason \"...\" describing why you need more time, and retry."
	case utils.WorkSessionExtensionAlreadyPending:
		return fmt.Sprintf("Work session %s already has an extension request pending approval; wait for it to be decided before asking again. Find its id with 'alpacon work-session describe %s --output json' (field pending_extension_request.id).", id, id)
	case utils.WorkSessionAdmissionDenied:
		return "This workspace refuses this extension outright; ask an admin to adjust the work-session admission policy."
	default:
		return fmt.Sprintf("Failed to extend work session: %s.", err)
	}
}
