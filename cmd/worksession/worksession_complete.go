package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionCompleteCmd = &cobra.Command{
	Use:     "complete SESSION_ID",
	Short:   "Mark an active work session as completed",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session complete ses-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opComplete, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		cleared, err := RunComplete(ac, args[0])
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opComplete, err, "Failed to complete work session: %s.", err)
		}

		utils.CliSuccess("Work session %s completed.", args[0])
		if cleared {
			utils.CliInfo("Active work-session cleared.")
		}
	},
}

// RunComplete completes the session; see endSession.
func RunComplete(ac *client.AlpaconClient, id string) (bool, error) {
	return endSession(ac, id, wsapi.CompleteWorkSession)
}
