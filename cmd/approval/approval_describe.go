package approval

import (
	"fmt"
	"slices"
	"strings"

	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type describeRow struct {
	Field string `table:"Field"`
	Value string `table:"Value"`
}

var approvalDescribeCmd = &cobra.Command{
	Use:     "describe REQUEST_ID",
	Aliases: []string{"desc"},
	Short:   "Show details of an approval request",
	Long: `Show an approval request.

The table shows the decision context the server sends for the request type:
the command or sudo detail, its risk assessment, the work session and the
requested changes. Use --output json to print the full raw response.`,
	Args: cobra.ExactArgs(1),
	Example: `  alpacon approval describe apr-abc123
  alpacon approval desc apr-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			body, err := approvalapi.GetApprovalRequestRaw(ac, args[0])
			if err != nil {
				utils.CliErrorWithExit("Failed to retrieve approval request: %s.", err)
			}
			utils.PrintJson(body)
			return
		}

		req, err := approvalapi.GetApprovalRequest(ac, args[0])
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve approval request: %s.", err)
		}

		rows := describeRows(req)
		var script string
		var hasScript bool
		if fe := fileExecutionOf(req); fe != nil {
			script, hasScript = fileExecutionScript(fe)
		}
		utils.PrintTable(rows)
		if hasScript {
			fmt.Println()
			fmt.Println(script)
		}
	},
}

func describeRows(req *approvalapi.ApprovalRequest) []describeRow {
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
	rows = append(rows, contextRows(req.Metadata)...)
	if fe := fileExecutionOf(req); fe != nil {
		rows = append(rows, fileExecutionRows(fe)...)
	}
	return rows
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

// fileExecutionScript renders the script the approver is asked to clear. When
// the server omits the content, only the SHA-256 above identifies the file, and
// the text says so instead of showing an empty script.
func fileExecutionScript(fe *approvalapi.FileExecution) (string, bool) {
	if fe.Content == nil {
		return "Script: not included in this response; the SHA-256 above identifies the file.", true
	}
	body, _ := utils.SanitizeTerminalBlock(*fe.Content)
	return fmt.Sprintf("Script (%d bytes):\n%s", len(*fe.Content), body), true
}

func joinClean(items []string, sep string) string {
	kept := make([]string, 0, len(items))
	for _, it := range items {
		if it = utils.SanitizeTerminalText(it); it != "" {
			kept = append(kept, it)
		}
	}
	return strings.Join(kept, sep)
}

func contextRows(meta *approvalapi.ApprovalMetadata) []describeRow {
	if meta == nil {
		return nil
	}
	clean := utils.SanitizeTerminalText
	var rows []describeRow
	add := func(field, value string) {
		if value = clean(value); value != "" {
			rows = append(rows, describeRow{field, value})
		}
	}

	if ws := meta.WorkSession; ws != nil {
		value := clean(ws.Title)
		if detail := joinClean([]string{ws.Status, ws.ID}, ", "); detail != "" {
			value = strings.TrimSpace(value + " (" + detail + ")")
		}
		add("Work session", value)
	}
	add("Session purpose", meta.Intent)
	if c := meta.Command; c != nil {
		add("Command", c.Line)
		add("Server", c.ServerName)
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
		add("Risk factors", joinClean(factors, "; "))
	}
	if s := meta.Sudo; s != nil {
		add("Sudo command", s.Command)
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
		servers := make([]string, len(ch.Servers))
		for i, sv := range ch.Servers {
			servers[i] = sv.Name
		}
		add("  Servers", joinClean(servers, ", "))
		policies := make([]string, len(ch.SudoPolicies))
		for i, p := range ch.SudoPolicies {
			policies[i] = joinClean(p.Commands, ", ")
		}
		add("  Sudo policies", joinClean(policies, "; "))
		add("  Target account", ch.TargetAccountUsername)
		if len(rows) > start {
			rows = slices.Insert(rows, start, describeRow{"Requested changes", ""})
		}
	}
	return rows
}
