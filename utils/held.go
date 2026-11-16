package utils

import (
	"fmt"
	"time"
)

const heldStatusPathDescription = "Where to read the command's sudo_grant_status while waiting; once it reads authorized, re-run the command"

// HeldRequest is the command detail's open sudo approval. Null or an absent
// key on an older server decodes to nil.
type HeldRequest struct {
	ApprovalRequest string     `json:"approval_request"`
	ExpiresAt       *time.Time `json:"expires_at"` // Nil when the server knows no deadline.
	StatusPath      string     `json:"status_path"`
}

// Expired reports whether the deadline has passed, not when approval occurred.
func (h *HeldRequest) Expired() bool {
	return h != nil && h.ExpiresAt != nil && time.Now().After(*h.ExpiresAt)
}

// heldCarrier is implemented by an error that carries the held request the
// command detail it was built from named.
type heldCarrier interface {
	ErrorHeld() *HeldRequest
}

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
			return actions
		}
		actions = append(actions, NextAction{Description: fmt.Sprintf("The approval must be decided by %s", deadline)})
	}
	// This credential already read the command detail, so unlike refusal "next",
	// the held path remains a command under an API token.
	if isSafeAPIPathArgument(held.StatusPath) {
		actions = append(actions, NextAction{Command: "alpacon api " + held.StatusPath, Description: heldStatusPathDescription})
	}
	return actions
}
