package worksession

import (
	"fmt"
	"io"
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

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}

func serverDiffNames(servers []types.ServerSummary) []string {
	names := make([]string, len(servers))
	for i, s := range servers {
		name := s.Name
		if s.IsDeleted != nil && *s.IsDeleted {
			name += " (deleted)"
		}
		names[i] = name
	}
	return names
}

func formatScopeDiff(d *wsapi.ScopeDiff) string {
	return joinOrNone(d.Old) + " → " + joinOrNone(d.New)
}

func formatServerDiff(d *wsapi.ServerDiff) string {
	return joinOrNone(serverDiffNames(d.Old)) + " → " + joinOrNone(serverDiffNames(d.New))
}

func formatRecommendation(r wsapi.WorkSessionRecommendation) string {
	return fmt.Sprintf("[%s] (%s) %s", r.Severity, r.Source, r.Text)
}

// writeApprovalNotice surfaces approver adjustments/recommendations after a
// --wait approval so the requester sees any reduced scope. No-op when absent.
func writeApprovalNotice(w io.Writer, s *wsapi.WorkSession) {
	adj := s.Adjustments
	if adj != nil && (adj.Scopes != nil || adj.Servers != nil) {
		_, _ = fmt.Fprintln(w, utils.Yellow("⚠ Approver adjusted your request:"))
		if adj.Scopes != nil {
			_, _ = fmt.Fprintf(w, "  scopes:  %s\n", formatScopeDiff(adj.Scopes))
		}
		if adj.Servers != nil {
			_, _ = fmt.Fprintf(w, "  servers: %s\n", formatServerDiff(adj.Servers))
		}
	}
	if len(s.Recommendations) > 0 {
		_, _ = fmt.Fprintln(w, "Recommendations:")
		for _, r := range s.Recommendations {
			_, _ = fmt.Fprintf(w, "  %s\n", formatRecommendation(r))
		}
	}
}
