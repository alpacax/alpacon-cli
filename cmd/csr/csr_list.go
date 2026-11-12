package csr

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var csrListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List certificate signing requests",
	Long: `
	Display CSRs, optionally filtered by status ('requested', 'signing', 'signed',
    'issued', 'failed', 'canceled', 'denied'). Use the --status flag to specify the status.
	`,
	Example: `
	alpacon csr ls
	alpacon csr list
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		status, _ := cmd.Flags().GetString("status")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		csrList, err := cert.GetCSRList(alpaconClient, status)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the csr list: %s.", err)
		}

		utils.PrintTable(csrList)
		return nil
	}),
}

func init() {
	csrListCmd.Flags().StringP("status", "s", "", "Specify the status of the CSR (e.g., 'denied', 'signed')")
}
