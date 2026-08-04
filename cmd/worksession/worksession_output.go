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

func newWorkSessionCancelOutput(id string) workSessionMutationOutput {
	return workSessionMutationOutput{
		OK:            true,
		Operation:     opCancel,
		Message:       fmt.Sprintf("Work session %s cancelled.", id),
		WorkSessionID: id,
		Status:        cancelledWorkSessionStatus,
	}
}

func formatMutationExpiresAt(expiresAt time.Time) string {
	if expiresAt.IsZero() {
		return ""
	}
	return expiresAt.UTC().Format(time.RFC3339)
}

// The success lines below echo values the API stores unchecked, the description
// most of all, and CliSuccess writes them straight to the terminal.
func createSuccessMessage(session *wsapi.WorkSession) string {
	if session.ApprovalRequestID != "" {
		return fmt.Sprintf("Work session created: %s (status: %s, approval request: %s)",
			utils.SanitizeTerminalText(session.ID),
			utils.SanitizeTerminalText(session.Status),
			utils.SanitizeTerminalText(session.ApprovalRequestID))
	}
	return fmt.Sprintf("Work session created: %s (status: %s)",
		utils.SanitizeTerminalText(session.ID), utils.SanitizeTerminalText(session.Status))
}

func updateSuccessMessage(session *wsapi.WorkSession) string {
	return fmt.Sprintf("Work session %s updated (status: %s).",
		utils.SanitizeTerminalText(session.ID), utils.SanitizeTerminalText(session.Status))
}

func activeWorkSessionSetMessage(successPrefix, id, desc string) string {
	suffix := ""
	// Stripped first: a description of nothing but control bytes would print as
	// empty parentheses.
	if stripped := utils.SanitizeTerminalText(desc); stripped != "" {
		suffix = fmt.Sprintf(" (%s)", stripped)
	}
	return fmt.Sprintf("%sActive work-session set to %s%s.",
		utils.SanitizeTerminalText(successPrefix), utils.SanitizeTerminalText(id), suffix)
}

func printWorkSessionMutationJSON(output workSessionMutationOutput) {
	if err := utils.PrintJSONValue(os.Stdout, output); err != nil {
		// Keeps stderr structured; PrintJSONError falls back to minimal JSON if marshalling fails again.
		utils.CliErrorEnvelopeWithExit(output.Operation, err, "Failed to marshal work-session result: %s", err)
	}
}

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

// The approver writes this text freely and the server stores it unchecked, so a
// control sequence here rewrites the requester's terminal.
func formatRecommendations(recs []wsapi.Recommendation) string {
	if len(recs) == 0 {
		return ""
	}
	lines := make([]string, len(recs))
	for i, r := range recs {
		// Fall back after stripping: a severity made only of control bytes is
		// empty by the time it reaches the format string.
		sev := utils.SanitizeTerminalText(r.Severity)
		if sev == "" {
			sev = "info"
		}
		lines[i] = fmt.Sprintf("  [%s] %s", strings.ToUpper(sev), utils.SanitizeTerminalText(r.Text))
	}
	return strings.Join(lines, "\n")
}

// Text mode only — JSON callers return before reaching this.
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
	sanitized := make([]string, len(items))
	for i, item := range items {
		sanitized[i] = utils.SanitizeTerminalText(item)
	}
	return strings.Join(sanitized, ", ")
}

func joinServerNames(servers []types.ServerSummary) string {
	if len(servers) == 0 {
		return "none"
	}
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = utils.SanitizeTerminalText(s.Name)
	}
	return strings.Join(names, ", ")
}
