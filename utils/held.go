package utils

import (
	"fmt"
	"time"
)

// HeldRequest is the open approval that decides a command's sudo, as the
// command detail's "held" object names it; nil when the detail sends null or,
// on an older server, no key at all.
type HeldRequest struct {
	ApprovalRequest string `json:"approval_request"`
	// ExpiresAt is nil when the server knows no deadline.
	ExpiresAt  *time.Time `json:"expires_at"`
	StatusPath string     `json:"status_path"`
}

// heldCarrier is implemented by an error that carries the held request the
// command detail it was built from named.
type heldCarrier interface {
	ErrorHeld() *HeldRequest
}

const heldStatusPathDescription = "Where to read the command's sudo_grant_status while waiting; once it reads authorized, re-run the command"

// Expired reports whether the hold's deadline has passed: an approval given
// after it cannot be used by a re-run, so a new approval is needed.
func (h *HeldRequest) Expired() bool {
	return h != nil && h.ExpiresAt != nil && time.Now().After(*h.ExpiresAt)
}

// heldNextActions renders the first held request in err's chain: the approval
// request as a reference, its deadline, and the status path as where to wait.
// The path is checked like a refusal's "next" but stays a command under an API
// token, since that credential has just read the same route.
func heldNextActions(err error) []NextAction {
	var held *HeldRequest
	for e := range errorChain(err) {
		if carrier, ok := e.(heldCarrier); ok {
			if held = carrier.ErrorHeld(); held != nil {
				break
			}
		}
	}
	if held == nil {
		return nil
	}
	var actions []NextAction
	if ref := SanitizeServerHint(held.ApprovalRequest); ref != "" {
		actions = append(actions, NextAction{Description: "Approval request: " + ref})
	}
	if held.ExpiresAt != nil {
		deadline := held.ExpiresAt.Format(time.RFC3339)
		if held.Expired() {
			actions = append(actions, NextAction{Description: fmt.Sprintf("The approval deadline (%s) has passed; an approval given now cannot be used, so re-run the command to request a new one", deadline)})
		} else {
			actions = append(actions, NextAction{Description: fmt.Sprintf("The approval must be decided by %s", deadline)})
		}
	}
	if isSafeAPIPathArgument(held.StatusPath) {
		actions = append(actions, NextAction{Command: "alpacon api " + held.StatusPath, Description: heldStatusPathDescription})
	}
	return actions
}
