package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionCancelCmd = &cobra.Command{
	Use:     "cancel SESSION_ID",
	Short:   "Withdraw your own pending work session request",
	Long:    "Withdraw a pending work session you requested before it is reviewed. Restricted to the session's requester (or a superuser); only pending sessions can be cancelled.",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session cancel 550e8400-e29b-41d4-a716-446655440000`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opCancel, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := wsapi.CancelWorkSession(ac, args[0]); err != nil {
			utils.CliErrorEnvelopeWithExit(opCancel, err, "Failed to cancel work session: %s.", err)
		}

		output := newWorkSessionCancelOutput(args[0])
		if utils.OutputFormat == utils.OutputFormatJSON {
			printWorkSessionMutationJSON(output)
			return
		}
		utils.CliSuccess("%s", output.Message)
	},
}
