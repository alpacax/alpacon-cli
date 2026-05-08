package worksession

import (
	"github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List work sessions",
	Example: `  alpacon work-session list
  alpacon work-session ls --status active
  alpacon work-session list --requester-type agent`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		sessions, err := worksession.GetWorkSessionList(ac, statusFilter, requesterFilter)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve work sessions: %s.", err)
		}

		utils.PrintTable(sessions)
	},
}

func init() {
	workSessionListCmd.Flags().StringVar(&statusFilter, "status", "", "Filter by status (pending, approved, active, completed, rejected, expired, revoked)")
	workSessionListCmd.Flags().StringVar(&requesterFilter, "requester-type", "", "Filter by requester type (user, agent)")
}
