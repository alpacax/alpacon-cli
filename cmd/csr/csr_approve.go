package csr

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var csrApproveCmd = &cobra.Command{
	Use:   "approve CSR_ID",
	Short: "Approve a CSR",
	Long: `
	Reviews and approves a pending Certificate Signing Request, 
	moving it forward in the signing process to eventually be issued as a valid certificate.
	`,
	Example: `alpacon csr approve 550e8400-e29b-41d4-a716-446655440000`,
	Args:    cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		csrId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		_, err = cert.ApproveCSR(alpaconClient, csrId)
		if err != nil {
			return utils.NewExitError("Failed to approve the csr: %s.", err)
		}

		utils.CliSuccess("CSR approved. Run 'alpacon csr ls' to verify status.")
		return nil
	}),
}
