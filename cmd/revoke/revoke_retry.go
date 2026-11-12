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
	Example: `alpacon revoke retry 550e8400-e29b-41d4-a716-446655440000`,
	Args:    cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		requestId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		_, err = cert.RetryRevokeRequest(alpaconClient, requestId)
		if err != nil {
			return utils.NewExitError("Failed to retry the revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request retried. Run 'alpacon revoke ls' to verify status.")
		return nil
	}),
}
