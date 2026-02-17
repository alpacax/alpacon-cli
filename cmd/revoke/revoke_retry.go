package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeRetryCmd = &cobra.Command{
	Use:   "retry REQUEST_ID",
	Short: "Retry a failed revoke request",
	Long: `
	Retries a previously failed certificate revoke request,
	resubmitting it for processing in the revocation pipeline.
	`,
	Example: `alpacon revoke retry REQUEST_ID`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		requestId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		_, err = cert.RetryRevokeRequest(alpaconClient, requestId)
		if err != nil {
			utils.CliErrorWithExit("Failed to retry the revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request retried. Run 'alpacon revoke ls' to verify status.")
	},
}
