package csr

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var csrApproveCmd = &cobra.Command{
	Use:   "approve",
	Short: "Approve a CSR",
	Long: `
	Reviews and approves a pending Certificate Signing Request, 
	moving it forward in the signing process to eventually be issued as a valid certificate.
	`,
	Example: `alpacon csr approve [CSR ID] `,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		csrId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		_, err = cert.ApproveCSR(alpaconClient, csrId)
		if err != nil {
			utils.CliErrorWithExit("Failed to approve the csr: %s.", err)
		}

		utils.CliInfo("CSR approval request successful. Please verify the CSR status.")
	},
}
