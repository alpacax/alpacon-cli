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
	Short:   "List certificates",
	Long: `
	Retrieves and shows a detailed list of all the SSL/TLS certificates currently managed by the system, 
	including their issuance status and validity.
	`,
	Example: `
	alpacon cert ls
	alpacon cert list
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		certList, err := cert.GetCertificateList(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the certificate list: %s.", err)
		}

		return utils.PrintTable(certList)
	}),
}
