package audit

import (
	"github.com/alpacax/alpacon-cli/api/audit"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var AuditCmd = &cobra.Command{
	Use:     "audit",
	Aliases: []string{"audit-log"},
	Short:   "Retrieve and display audit activity logs",
	Long: `
	Retrieve and display audit activity logs from the Alpacon, with options to filter by user,
	application, and model. Use the '--tail' flag to limit the output to the last N entries.
	`,
	Example: `
	alpacon audit
	alpacon audit-log
	alpacon audit --tail 10 --user admin
	alpacon audit --tail=50 --app=cert --model=authority
	`,
	Run: runAudit,
}

func init() {
	var pageSize int
	var userName string
	var app string
	var model string

	AuditCmd.Flags().IntVarP(&pageSize, "tail", "t", 25, "Number of audit log entries to show from the end")
	AuditCmd.Flags().StringVarP(&userName, "user", "u", "", "Filter by username")
	AuditCmd.Flags().StringVarP(&app, "app", "a", "", "Filter by application")
	AuditCmd.Flags().StringVarP(&model, "model", "m", "", "Filter by model")
}

func runAudit(cmd *cobra.Command, args []string) {
	pageSize, _ := cmd.Flags().GetInt("tail")
	userName, _ := cmd.Flags().GetString("user")
	app, _ := cmd.Flags().GetString("app")
	model, _ := cmd.Flags().GetString("model")

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	auditList, err := audit.GetAuditLogList(alpaconClient, pageSize, userName, app, model)
	if err != nil {
		utils.CliErrorWithExit("Failed to get audit logs: %s.", err)
	}

	utils.PrintTable(auditList)
}
