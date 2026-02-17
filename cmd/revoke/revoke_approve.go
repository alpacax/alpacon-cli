package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeApproveCmd = &cobra.Command{
	Use:   "approve [REQUEST ID]",
	Short: "Approve a revoke request",
	Long: `
	Approves a pending certificate revoke request, moving it forward in the
	revocation process to eventually revoke the certificate.
	`,
	Example: `alpacon revoke approve [REQUEST ID]`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		requestId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		_, err = cert.ApproveRevokeRequest(alpaconClient, requestId)
		if err != nil {
			utils.CliErrorWithExit("Failed to approve the revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request approved. Run 'alpacon revoke ls' to verify status.")
	},
}
