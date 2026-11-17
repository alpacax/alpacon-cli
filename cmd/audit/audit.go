package audit

import (
	"github.com/alpacax/alpacon-cli/api/audit"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

var AuditCmd = newAuditCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newAuditCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "audit",
		Aliases: []string{"audit-log"},
		Short:   "Retrieve and display audit activity logs",
		Long: `
	Retrieve and display audit activity logs from the Alpacon, with options to filter by user,
	application, and model. Use the '--tail' flag to show the newest N entries.
	`,
		Example: `
	alpacon audit
	alpacon audit-log
	alpacon audit --tail 10 --user admin
	alpacon audit --tail=50 --app=cert --model=authority
	`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runAudit(ios, cmd, args, newClient)
		}),
	}
	var model string
	var app string
	var userName string
	var tail int
	cmd.Flags().IntVarP(&tail, "tail", "t", 25, "Number of audit log entries to show, newest first")
	cmd.Flags().StringVarP(&userName, "user", "u", "", "Filter by username")
	cmd.Flags().StringVarP(&app, "app", "a", "", "Filter by application")
	cmd.Flags().StringVarP(&model, "model", "m", "", "Filter by model")
	return cmd
}

func runAudit(ios *utils.IOStreams, cmd *cobra.Command, args []string, newClient clientFactory) error {
	tail, _ := cmd.Flags().GetInt("tail")
	if err := utils.CheckPositiveInt("tail", tail); err != nil {
		return err
	}
	userName, _ := cmd.Flags().GetString("user")
	app, _ := cmd.Flags().GetString("app")
	model, _ := cmd.Flags().GetString("model")

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	auditList, err := audit.GetAuditLogList(alpaconClient, tail, userName, app, model)
	return utils.PrintListTo(ios, auditList, err, "Failed to get audit logs")
}
