package csr

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var csrDeleteCmd = &cobra.Command{
	Use:     "delete CSR_ID",
	Aliases: []string{"rm"},
	Short:   "Delete a CSR",
	Long: `
 	Removes a Certificate Signing Request from the system, 
	effectively canceling the request and any associated processing.
	`,
	Example: `
	alpacon csr delete 550e8400-e29b-41d4-a716-446655440000
	alpacon csr rm 550e8400-e29b-41d4-a716-446655440000
	alpacon csr delete 550e8400-e29b-41d4-a716-446655440000 -y
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		csrId := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete CSR '%s'?", csrId)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = cert.DeleteCSR(alpaconClient, csrId)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the CSR: %s.", err)
		}

		utils.CliSuccess("CSR deleted: %s", csrId)
	},
}

func init() {
	csrDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
