package csr

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var csrDenyCmd = &cobra.Command{
	Use:   "deny",
	Short: "Deny a CSR",
	Long: `
	Rejects a Certificate Signing Request, marking it as denied and stopping any further processing 
	or signing activities for that request
	`,
	Example: `alpacon csr deny 550e8400-e29b-41d4-a716-446655440000`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		csrId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		_, err = cert.DenyCSR(alpaconClient, csrId)
		if err != nil {
			utils.CliErrorWithExit("Failed to deny the csr: %s.", err)
		}

		utils.CliSuccess("CSR denied. Run 'alpacon csr ls' to verify status.")
	},
}
