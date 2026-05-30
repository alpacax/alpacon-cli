package worksession

import (
	"fmt"
	"os"
	"time"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/utils"
)

type workSessionMutationOutput struct {
	OK                bool               `json:"ok"`
	Operation         string             `json:"operation"`
	Message           string             `json:"message"`
	WorkSessionID     string             `json:"work_session_id,omitempty"`
	Status            string             `json:"status,omitempty"`
	ExpiresAt         string             `json:"expires_at,omitempty"`
	ApprovalRequestID string             `json:"approval_request_id,omitempty"`
	ActiveWorksession *string            `json:"active_worksession"`
	WorkSession       *wsapi.WorkSession `json:"work_session,omitempty"`
}

func newWorkSessionMutationOutput(operation, message string, session *wsapi.WorkSession, activeWorksession *string) workSessionMutationOutput {
	output := workSessionMutationOutput{
		OK:                true,
		Operation:         operation,
		Message:           message,
		ActiveWorksession: activeWorksession,
		WorkSession:       session,
	}
	if session != nil {
		output.WorkSessionID = session.ID
		output.Status = session.Status
		output.ApprovalRequestID = session.ApprovalRequestID
		output.ExpiresAt = formatMutationExpiresAt(session.ExpiresAt)
	}
	return output
}

func newWorkSessionExtendOutput(id, expiresAt string) workSessionMutationOutput {
	return workSessionMutationOutput{
		OK:            true,
		Operation:     "extend",
		Message:       fmt.Sprintf("Work session %s extended to %s.", id, expiresAt),
		WorkSessionID: id,
		ExpiresAt:     expiresAt,
	}
}

func formatMutationExpiresAt(expiresAt time.Time) string {
	if expiresAt.IsZero() {
		return ""
	}
	return expiresAt.UTC().Format(time.RFC3339)
}

func createSuccessMessage(session *wsapi.WorkSession) string {
	if session.ApprovalRequestID != "" {
		return fmt.Sprintf("Work session created: %s (status: %s, approval request: %s)", session.ID, session.Status, session.ApprovalRequestID)
	}
	return fmt.Sprintf("Work session created: %s (status: %s)", session.ID, session.Status)
}

func activeWorkSessionSetMessage(successPrefix, id, desc string) string {
	suffix := ""
	if desc != "" {
		suffix = fmt.Sprintf(" (%s)", desc)
	}
	return fmt.Sprintf("%sActive work-session set to %s%s.", successPrefix, id, suffix)
}

func printWorkSessionMutationJSON(output workSessionMutationOutput) {
	if err := utils.PrintJSONValue(os.Stdout, output); err != nil {
		utils.CliErrorWithExit("Failed to marshal work-session result: %s", err)
	}
}
