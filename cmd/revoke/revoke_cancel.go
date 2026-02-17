package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeCancelCmd = &cobra.Command{
	Use:   "cancel [REQUEST ID]",
	Short: "Cancel a revoke request",
	Long: `
	Cancels a pending certificate revoke request by deleting it.
	This action is irreversible.
	`,
	Example: `
	alpacon revoke cancel [REQUEST ID]
	alpacon revoke cancel [REQUEST ID] -y
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		requestId := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Cancel revoke request '%s'?", requestId)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = cert.CancelRevokeRequest(alpaconClient, requestId)
		if err != nil {
			utils.CliErrorWithExit("Failed to cancel the revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request cancelled: %s", requestId)
	},
}

func init() {
	revokeCancelCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
