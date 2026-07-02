package worksession

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
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
		Operation:     opExtend,
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
		// Keeps stderr structured; PrintJSONError falls back to minimal JSON if marshalling fails again.
		utils.CliErrorEnvelopeWithExit(output.Operation, err, "Failed to marshal work-session result: %s", err)
	}
}

// formatAdjustments renders the approver's scope/server diff, or "" when there was no adjustment.
func formatAdjustments(adj *wsapi.Adjustments) string {
	if adj == nil {
		return ""
	}
	var lines []string
	if adj.Scopes != nil {
		lines = append(lines, fmt.Sprintf("  scopes:  %s → %s",
			joinOrNone(adj.Scopes.Old), joinOrNone(adj.Scopes.New)))
	}
	if adj.Servers != nil {
		lines = append(lines, fmt.Sprintf("  servers: %s → %s",
			joinServerNames(adj.Servers.Old), joinServerNames(adj.Servers.New)))
	}
	return strings.Join(lines, "\n")
}

// formatRecommendations renders one "[SEVERITY] text" line per recommendation, or "" when there are none.
func formatRecommendations(recs []wsapi.Recommendation) string {
	if len(recs) == 0 {
		return ""
	}
	lines := make([]string, len(recs))
	for i, r := range recs {
		lines[i] = fmt.Sprintf("  [%s] %s", strings.ToUpper(r.Severity), r.Text)
	}
	return strings.Join(lines, "\n")
}

// printSessionAdvisories prints adjustments/recommendations; text mode only, JSON callers return earlier.
func printSessionAdvisories(session *wsapi.WorkSession) {
	if block := formatAdjustments(session.Adjustments); block != "" {
		utils.CliWarning("Approver adjusted your request:\n%s", block)
	}
	if block := formatRecommendations(session.Recommendations); block != "" {
		utils.CliInfo("Recommendations from approver:\n%s", block)
	}
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

func joinServerNames(servers []types.ServerSummary) string {
	if len(servers) == 0 {
		return "none"
	}
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}
