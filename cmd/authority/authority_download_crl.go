package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityDownloadCrlCmd = &cobra.Command{
	Use:   "download-crl AUTHORITY_ID",
	Short: "Download a certificate revocation list (CRL)",
	Long: `
	Download the certificate revocation list (CRL) from the specified authority and save it to a file.
	The path argument should include the file name and extension where the CRL will be stored.
	For example, '/path/to/revoked.crl'. The recommended file extension is '.crl'.`,
	Example: `
	alpacon authority download-crl AUTHORITY_ID --out=/path/to/revoked.crl
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		authorityId := args[0]
		filePath, _ := cmd.Flags().GetString("out")
		if filePath == "" {
			filePath = promptForCRL()
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = cert.DownloadCRL(alpaconClient, authorityId, filePath)
		if err != nil {
			utils.CliErrorWithExit("Failed to download the CRL from authority: %s.", err)
		}

		utils.CliSuccess("CRL downloaded: %s", filePath)
	},
}

func init() {
	var filePath string
	authorityDownloadCrlCmd.Flags().StringVarP(&filePath, "out", "o", "", "path where CRL should be stored")
}

func promptForCRL() string {
	return utils.PromptForRequiredInput("Path to CRL file (e.g., /path/to/revoked.crl, recommended extension: .crl): ")
}
