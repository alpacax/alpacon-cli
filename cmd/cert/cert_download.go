package cert

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var certDownloadCmd = &cobra.Command{
	Use:   "download [CERT ID]",
	Short: "Download a certificate",
	Long: `
	Download a certificate from the server and save it to a specified file path. 
	The path argument should include the file name and extension where the certificate will be stored. 
	For example, '/path/to/certificate.crt'. The recommended file extension for certificates is '.crt'.`,
	Example: `
	alpacon cert download [CERT ID] --out=/path/to/certificate.crt
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		certId := args[0]
		filePath, _ := cmd.Flags().GetString("out")
		if filePath == "" {
			filePath = promptForCertificate()
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = cert.DownloadCertificate(alpaconClient, certId, filePath)
		if err != nil {
			utils.CliErrorWithExit("Failed to download the certificate from authority: %s.", err)
		}

		utils.CliSuccess("Certificate downloaded: %s", filePath)
	},
}

func init() {
	var filePath string
	certDownloadCmd.Flags().StringVarP(&filePath, "out", "o", "", "path where certificate should be stored")
}

func promptForCertificate() string {
	return utils.PromptForRequiredInput("Path to certificate (e.g., /path/to/certificate.crt, recommended extension: .crt): ")
}
