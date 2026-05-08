package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List work sessions",
	Example: `  alpacon work-session ls
  alpacon work-session ls --status active
  alpacon work-session ls --requester-type agent`,
	Run: func(cmd *cobra.Command, args []string) {
		if requesterFilter != "" && requesterFilter != "user" && requesterFilter != "agent" {
			utils.CliErrorWithExit("Invalid --requester-type %q: must be \"user\" or \"agent\".", requesterFilter)
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		sessions, err := wsapi.GetWorkSessionList(ac, statusFilter, requesterFilter)
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
