package csr

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var csrDeleteCmd = &cobra.Command{
	Use:     "delete [CSR ID]",
	Aliases: []string{"rm"},
	Short:   "Delete a CSR",
	Long: `
 	Removes a Certificate Signing Request from the system, 
	effectively canceling the request and any associated processing.
	`,
	Example: ` 
	alpacon csr delete [CSR ID]	
	alpacon csr rm [CSR ID]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		csrId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = cert.DeleteCSR(alpaconClient, csrId)
		if err != nil {
			utils.CliError("Failed to delete the CSR: %s. ", err)
		}

		utils.CliInfo("CSR successfully deleted: %s.", csrId)
	},
}
