package approval

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type describeRow struct {
	Field string `table:"Field"`
	Value string `table:"Value"`
}

func newApprovalDescribeCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:     "describe REQUEST_ID",
		Aliases: []string{"desc"},
		Short:   "Show details of an approval request",
		Long: `Show an approval request.

The table shows the decision context the server sends for the request type:
the command or sudo detail, its risk assessment, the work session and the
requested changes. For a file execution, the AI evidence rows list each claim
behind the AI's verdict beside the script lines it rests on (L3, L40-41); a
claim marked Not placed could not be located in the script. When at least one
claim is placed, the script below is printed with line numbers. Use --output
json to print the full raw response.`,
		Args: cobra.ExactArgs(1),
		Example: `  alpacon approval describe 550e8400-e29b-41d4-a716-446655440000
  alpacon approval desc 550e8400-e29b-41d4-a716-446655440000`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			ac, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			if ios.IsJSON() {
				body, err := approvalapi.GetApprovalRequestRaw(ac, args[0])
				if err != nil {
					return utils.NewExitError("Failed to retrieve approval request: %s.", err)
				}
				return ios.PrintJson(body)
			}

			req, err := approvalapi.GetApprovalRequest(ac, args[0])
			if err != nil {
				return utils.NewExitError("Failed to retrieve approval request: %s.", err)
			}

			fe := fileExecutionOf(req)
			evidence, placed := evidenceRows(req)
			rows := append(describeRows(req, fe), evidence...)
			if err := ios.PrintTable(rows); err != nil {
				return err
			}
			if fe != nil {
				script, altered := fileExecutionScript(fe, placed)
				_, _ = fmt.Fprintln(ios.Out)
				if altered {
					// The warning stays on Out beside the script it is about, and
					// ColorEnabled follows ErrOut, so it is written without color.
					plain := *ios
					plain.ColorEnabled = false
					plain.WarnTerminalTextAltered(ios.Out, "")
				}
				_, _ = fmt.Fprintln(ios.Out, script)
			}
			return nil
		}),
	}
}

func describeRows(req *approvalapi.ApprovalRequest, fe *approvalapi.FileExecution) []describeRow {
	requestedBy := ""
	if req.RequestedBy != nil {
		requestedBy = req.RequestedBy.Name
	}
	reviewedBy := ""
	if req.ReviewedBy != nil {
		reviewedBy = req.ReviewedBy.Name
	}
	reviewedAt := ""
	if req.ReviewedAt != nil {
		reviewedAt = req.ReviewedAt.Local().Format("2006-01-02 15:04")
	}

	rows := []describeRow{
		{"ID", req.ID},
		{"Type", req.RequestType},
		{"Status", req.Status},
		{"Request", req.RequestData},
		{"Description", req.Description},
		{"Requested by", requestedBy},
		{"Reviewed by", reviewedBy},
		{"Reviewed at", reviewedAt},
		{"Added at", req.AddedAt.Local().Format("2006-01-02 15:04")},
	}
	if note := utils.SanitizeTerminalOneLine(req.RejectionNote); note != "" {
		// The approver's own words, stripped of terminal escapes like any server value.
		rows = append(rows, describeRow{"Rejection reason", note})
	}
	rows = append(rows, contextRows(req.Metadata)...)
	if fe != nil {
		rows = append(rows, fileExecutionRows(fe)...)
	}
	return rows
}

// evidenceRows are the "AI evidence" rows: each claim beside the script lines
// it rests on. As in the console, a range the script does not have and a
// repeated one are left out, and a claim left with none reads as not placed.
// placed reports whether any claim kept a range.
func evidenceRows(req *approvalapi.ApprovalRequest) (rows []describeRow, placed bool) {
	if req.Metadata == nil || req.Metadata.Command == nil {
		return nil, false
	}
	c := req.Metadata.Command
	// Counted as the server counts the lines its ranges point at. The server
	// sends no evidence with a withheld script, so a zero count is only a guard.
	lineCount := 0
	if fe := c.FileExecution; fe != nil && fe.Content != nil {
		lineCount = strings.Count(*fe.Content, "\n") + 1
	}
	for _, ev := range c.VerdictEvidence {
		claim := utils.SanitizeTerminalOneLine(ev.Claim)
		if claim == "" {
			continue
		}
		var spans []string
		seen := map[approvalapi.EvidenceRange]bool{}
		for _, r := range ev.Ranges {
			if r.EndLine > lineCount || seen[r] {
				continue
			}
			seen[r] = true
			span := fmt.Sprintf("L%d", r.StartLine)
			if r.EndLine > r.StartLine {
				span += fmt.Sprintf("-%d", r.EndLine)
			}
			spans = append(spans, span)
		}
		if len(spans) == 0 {
			rows = append(rows, describeRow{"  Not placed", claim + " (could not be placed in the script)"})
			continue
		}
		placed = true
		rows = append(rows, describeRow{"  " + strings.Join(spans, ", "), claim})
	}
	if len(rows) > 0 {
		rows = slices.Insert(rows, 0, describeRow{"AI evidence", ""})
	}
	return rows, placed
}

func fileExecutionOf(req *approvalapi.ApprovalRequest) *approvalapi.FileExecution {
	if req.Metadata == nil || req.Metadata.Command == nil {
		return nil
	}
	return req.Metadata.Command.FileExecution
}

// fileExecutionRows are the "File execution" rows of the describe table. The
// server controls every value, so each is stripped of terminal escapes.
func fileExecutionRows(fe *approvalapi.FileExecution) []describeRow {
	clean := utils.SanitizeTerminalText
	args := make([]string, len(fe.Args))
	for i, a := range fe.Args {
		args[i] = clean(a)
	}
	rows := []describeRow{
		{"File execution", ""},
		{"  Interpreter", clean(fe.Interpreter)},
		{"  Path", clean(fe.Path)},
		{"  Arguments", strings.Join(args, " ")},
	}
	if fe.RunAs != "" {
		rows = append(rows, describeRow{"  Run as", clean(fe.RunAs)})
	}
	if fe.RunAsGroup != "" {
		rows = append(rows, describeRow{"  Run as group", clean(fe.RunAsGroup)})
	}
	rows = append(rows, describeRow{"  SHA-256", clean(fe.SHA256)})
	if fe.ReuseDays != nil {
		rows = append(rows, describeRow{"  Proposed reuse days", fmt.Sprintf("%d", *fe.ReuseDays)})
	}
	if fe.GrantDays != nil {
		rows = append(rows, describeRow{"  Reuse days if approved", fmt.Sprintf("%d", *fe.GrantDays)})
	}
	return rows
}

// fileExecutionScript renders the script for approval. numbered prefixes line
// numbers, including the final empty line, so evidence ranges can be found.
// altered reports removals beyond CRLF normalization and is false for withheld
// content.
func fileExecutionScript(fe *approvalapi.FileExecution, numbered bool) (script string, altered bool) {
	if fe.Content == nil {
		return "Script: not included in this response; the SHA-256 above identifies the file.", false
	}
	body, altered := sanitizeScript(*fe.Content)
	if numbered {
		lines := strings.Split(body, "\n")
		width := len(strconv.Itoa(len(lines)))
		for i, line := range lines {
			numberedLine := fmt.Sprintf("%*d |", width, i+1)
			if line != "" {
				numberedLine += " " + line
			}
			lines[i] = numberedLine
		}
		body = strings.Join(lines, "\n")
	}
	return fmt.Sprintf("Script (%d bytes):\n%s", len(*fe.Content), body), altered
}

// sanitizeScript keeps tabs, since they are part of the script the request
// runs, and still removes escape sequences, control characters and Unicode
// format characters so the terminal does not act on them. altered reports a
// removal other than folding CRLF into LF, so DOS line endings alone do not
// count.
func sanitizeScript(content string) (clean string, altered bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	clean = utils.SanitizeTerminalBlockKeepingTabs(normalized)
	return clean, clean != normalized
}

func joinClean(items []string, sep string) string {
	cleaned := make([]string, len(items))
	for i, it := range items {
		cleaned[i] = utils.SanitizeTerminalText(it)
	}
	return joinNonEmpty(cleaned, sep)
}

func joinNonEmpty(items []string, sep string) string {
	return strings.Join(slices.DeleteFunc(items, func(it string) bool { return it == "" }), sep)
}

func contextRows(meta *approvalapi.ApprovalMetadata) []describeRow {
	if meta == nil {
		return nil
	}
	var rows []describeRow
	add := func(field, value string) {
		if value = strings.TrimSpace(utils.SanitizeTerminalText(value)); value != "" {
			rows = append(rows, describeRow{field, value})
		}
	}

	if ws := meta.WorkSession; ws != nil {
		value := utils.SanitizeTerminalText(ws.Title)
		if detail := joinClean([]string{ws.Status, ws.ID}, ", "); detail != "" {
			value += " (" + detail + ")"
		}
		add("Work session", value)
	}
	add("Session purpose", meta.Intent)
	if c := meta.Command; c != nil {
		add("Command", c.Line)
		add("Server", c.ServerName)
		// A file execution shows its own run-as account under File execution,
		// and that is the one the console shows for it.
		if c.FileExecution == nil {
			add("Run as", c.RunAs)
		}
		add("Risk level", c.RiskLevel)
		if c.RunsWithSudo {
			add("Runs with sudo", "yes")
		}
		add("Purpose", c.Purpose)
		add("Declared agent", c.DeclaredAgent)
		add("Review summary", c.ReviewSummary)
		add("Reasoning", c.Tier2Reasoning)
		factors := make([]string, len(c.Factors))
		for i, f := range c.Factors {
			factors[i] = joinClean([]string{f.Type, f.Detail}, ": ")
		}
		add("Risk factors", joinNonEmpty(factors, "; "))
	}
	if s := meta.Sudo; s != nil {
		add("Sudo command", s.Command)
		add("Run as", s.RunAs)
		add("Purpose", s.Purpose)
		add("Server", s.ServerName)
		add("Risk level", s.RiskLevel)
		add("Grant status", s.GrantStatus)
		add("Declared agent", s.DeclaredAgent)
		add("Reasoning", s.Tier2Reasoning)
		if s.DescriptionAligned != nil {
			aligned := "no"
			if *s.DescriptionAligned {
				aligned = "yes"
			}
			add("Matches session purpose", aligned)
		}
	}
	if ch := meta.Changes; ch != nil {
		start := len(rows)
		add("  Title", ch.Title)
		add("  Description", ch.Description)
		add("  Scopes", joinClean(ch.Scopes, ", "))
		servers := make([]string, 0, len(ch.Servers))
		for _, sv := range ch.Servers {
			if name := utils.SanitizeTerminalText(sv.Name); name != "" {
				servers = append(servers, name)
			}
		}
		// The count keeps the row when every requested server is one the caller does not reach.
		add("  Servers", utils.JoinReached(servers, ch.ServersHidden, "server"))
		policies := make([]string, len(ch.SudoPolicies))
		for i, p := range ch.SudoPolicies {
			policies[i] = joinClean(p.Commands, ", ")
		}
		add("  Sudo policies", joinNonEmpty(policies, "; "))
		add("  Target account", ch.TargetAccountUsername)
		if len(rows) > start {
			rows = slices.Insert(rows, start, describeRow{"Requested changes", ""})
		}
	}
	return rows
}
