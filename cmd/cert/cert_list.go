package cert

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var certListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "Display a list of all certificates",
	Long: `
	Retrieves and shows a detailed list of all the SSL/TLS certificates currently managed by the system, 
	including their issuance status and validity.
	`,
	Example: `
	alpacon cert ls
	alpacon cert list
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		certList, err := cert.GetCertificateList(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the certificate list: %s.", err)
		}

		utils.PrintTable(certList)
	},
}
