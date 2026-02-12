package csr

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var csrListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "all"},
	Short:   "Display a list of all certificate signing requests",
	Long: `
	Display CSRs, optionally filtered by status ('requested', 'processing', 'signed',
    'issued', 'canceled', 'denied'). Use the --status flag to specify the status.
	`,
	Example: `
	alpacon csr ls
	alpacon csr list
	alpacon csr all
	`,
	Run: func(cmd *cobra.Command, args []string) {
		status, _ := cmd.Flags().GetString("status")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		csrList, err := cert.GetCSRList(alpaconClient, status)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the csr list: %s.", err)
		}

		utils.PrintTable(csrList)
	},
}

func init() {
	var state string

	csrListCmd.Flags().StringVarP(&state, "status", "s", "", "Specify the status of the CSR (e.g., 'denied', 'signed')")
}
