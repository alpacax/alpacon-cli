package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List certificate revoke requests",
	Long: `
	List all certificate revoke requests with optional filtering by status or certificate.
	`,
	Example: `
	alpacon revoke list
	alpacon revoke ls --status=pending
	alpacon revoke ls --certificate=550e8400-e29b-41d4-a716-446655440000
	`,
	Run: func(cmd *cobra.Command, args []string) {
		status, _ := cmd.Flags().GetString("status")
		certificate, _ := cmd.Flags().GetString("certificate")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		requestList, err := cert.GetRevokeRequestList(alpaconClient, status, certificate)
		if err != nil {
			utils.CliErrorWithExit("Failed to get revoke requests: %s.", err)
		}

		utils.PrintTable(requestList)
	},
}

func init() {
	revokeListCmd.Flags().String("status", "", "Filter by status (e.g., pending, approved, denied)")
	revokeListCmd.Flags().String("certificate", "", "Filter by certificate ID")
}
