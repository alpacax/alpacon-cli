package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeDetailCmd = &cobra.Command{
	Use:     "describe [REQUEST ID]",
	Aliases: []string{"desc"},
	Short:   "Display detailed information about a revoke request",
	Long: `
	The describe command fetches and displays detailed information about a specific certificate
	revoke request, including its status, reason, and associated certificate details.
	`,
	Example: `
	alpacon revoke describe 550e8400-e29b-41d4-a716-446655440000
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		requestId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		requestDetail, err := cert.GetRevokeRequestDetail(alpaconClient, requestId)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the revoke request details: %s.", err)
		}

		utils.PrintJson(requestDetail)
	},
}
