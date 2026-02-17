package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeDenyCmd = &cobra.Command{
	Use:   "deny [REQUEST ID]",
	Short: "Deny a revoke request",
	Long: `
	Denies a pending certificate revoke request, preventing the certificate from being revoked.
	`,
	Example: `alpacon revoke deny [REQUEST ID]`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		requestId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		_, err = cert.DenyRevokeRequest(alpaconClient, requestId)
		if err != nil {
			utils.CliErrorWithExit("Failed to deny the revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request denied. Run 'alpacon revoke ls' to verify status.")
	},
}
