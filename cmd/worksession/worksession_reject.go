package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionRejectCmd = &cobra.Command{
	Use:     "reject SESSION_ID",
	Short:   "Reject a pending work session",
	Long:    "Reject a pending work session. Superuser only.",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session reject ses-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := wsapi.RejectWorkSession(ac, args[0]); err != nil {
			utils.CliErrorWithExit("Failed to reject work session: %s.", err)
		}

		utils.CliSuccess("Work session %s rejected.", args[0])
	},
}
