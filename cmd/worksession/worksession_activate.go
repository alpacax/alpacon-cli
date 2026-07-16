package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionActivateCmd = &cobra.Command{
	Use:     "activate SESSION_ID",
	Short:   "Activate an approved work session",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session activate 550e8400-e29b-41d4-a716-446655440000`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opActivate, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := wsapi.ActivateWorkSession(ac, args[0]); err != nil {
			utils.CliErrorEnvelopeWithExit(opActivate, err, "Failed to activate work session: %s.", err)
		}

		utils.CliSuccess("Work session %s activated.", args[0])
	},
}
