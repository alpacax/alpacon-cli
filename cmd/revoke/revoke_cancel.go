package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeCancelCmd = &cobra.Command{
	Use:   "cancel REQUEST_ID",
	Short: "Cancel a revoke request",
	Long: `
	Cancels a pending certificate revoke request by deleting it.
	This action is irreversible.
	`,
	Example: `
	alpacon revoke cancel 550e8400-e29b-41d4-a716-446655440000
	alpacon revoke cancel 550e8400-e29b-41d4-a716-446655440000 -y
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		requestId := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			if proceed, err := utils.Confirm("Cancel revoke request '%s'?", requestId); err != nil || !proceed {
				return err
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = cert.CancelRevokeRequest(alpaconClient, requestId)
		if err != nil {
			return utils.NewExitError("Failed to cancel the revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request cancelled: %s", requestId)
		return nil
	}),
}

func init() {
	revokeCancelCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
