package event

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/utils"
)

// StandingGrantLine describes the grant recorded on a command detail.
func StandingGrantLine(details EventDetails) string {
	if details.ApprovedFileExecution == nil {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(*details.ApprovedFileExecution, &fields); err != nil {
		return ""
	}
	var grant struct {
		ID         string  `json:"id"`
		ExpiresAt  *string `json:"expires_at"`
		ApprovedBy *struct {
			Username string `json:"username"`
		} `json:"approved_by"`
		RevokedAt    *string `json:"revoked_at"`
		RevokeReason *string `json:"revoke_reason"`
	}
	if err := json.Unmarshal(*details.ApprovedFileExecution, &grant); err != nil {
		return ""
	}
	line := fmt.Sprintf("Allowed by a standing grant (%s)", utils.SanitizeTerminalText(grant.ID))
	_, hasExpiry := fields["expires_at"]
	_, hasApprover := fields["approved_by"]
	_, hasRequest := fields["approval_request"]
	_, hasRevocation := fields["revoked_at"]
	_, hasReason := fields["revoke_reason"]
	if !hasExpiry && !hasApprover && !hasRequest && !hasRevocation && !hasReason {
		return line
	}
	approver := "unknown"
	if grant.ApprovedBy != nil {
		approver = utils.SanitizeTerminalText(grant.ApprovedBy.Username)
	}
	expires := "never recorded"
	if grant.ExpiresAt != nil {
		expires = utils.FormatTimestamp(*grant.ExpiresAt)
	}
	line += fmt.Sprintf(" approved by %s, expires %s", approver, utils.SanitizeTerminalText(expires))
	if grant.RevokedAt != nil {
		line += "; revoked " + utils.SanitizeTerminalText(utils.FormatTimestamp(*grant.RevokedAt))
		if grant.RevokeReason != nil && *grant.RevokeReason != "" {
			line += ": " + utils.SanitizeTerminalText(*grant.RevokeReason)
		}
	}
	return line
}

func printStandingGrant(details EventDetails) {
	if line := StandingGrantLine(details); line != "" {
		_, _ = fmt.Fprintln(os.Stderr, line)
	}
}
