package cert

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var certDetailCmd = &cobra.Command{
	Use:     "describe [CERT ID]",
	Aliases: []string{"desc"},
	Short:   "Display detailed information about a specific Certificate",
	Long: `
	The describe command fetches and displays detailed information about a specific certificate, 
	including its crt text, certificate authority and other relevant attributes. 
	`,
	Example: ` 
	alpacon cert describe [CERT ID]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		certId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		certDetail, err := cert.GetCertificateDetail(alpaconClient, certId)
		if err != nil {
			utils.CliError("Failed to retrieve the certificate details: %s.", err)
		}

		utils.PrintJson(certDetail)
	},
}
