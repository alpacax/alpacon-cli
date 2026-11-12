package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeApproveCmd = &cobra.Command{
	Use:   "approve REQUEST_ID",
	Short: "Approve a revoke request",
	Long: `
	Approves a pending certificate revoke request, moving it forward in the
	revocation process to eventually revoke the certificate.
	`,
	Example: `alpacon revoke approve 550e8400-e29b-41d4-a716-446655440000`,
	Args:    cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		requestId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		_, err = cert.ApproveRevokeRequest(alpaconClient, requestId)
		if err != nil {
			return utils.NewExitError("Failed to approve the revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request approved. Run 'alpacon revoke ls' to verify status.")
		return nil
	}),
}
