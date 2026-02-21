package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityDownloadCmd = &cobra.Command{
	Use:     "download-crt AUTHORITY",
	Aliases: []string{"download-cert"},
	Short:   "Download a root certificate",
	Long: `
	Download a root certificate from the server and save it to a specified file path.
	The path argument should include the file name and extension where the certificate will be stored.
	For example, '/path/to/root.crt'. The recommended file extension for certificates is '.crt'.`,
	Example: `
	alpacon authority download-crt "Root CA" --out=/path/to/root.crt
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		authorityName := args[0]
		filePath, _ := cmd.Flags().GetString("out")
		if filePath == "" {
			filePath = promptForCertificate()
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authorityID, err := cert.GetAuthorityIDByName(alpaconClient, authorityName)
		if err != nil {
			utils.CliErrorWithExit("Failed to find authority: %s.", err)
		}

		err = cert.DownloadRootCertificate(alpaconClient, authorityID, filePath)
		if err != nil {
			utils.CliErrorWithExit("Failed to download the root certificate from authority: %s.", err)
		}

		utils.CliSuccess("Root certificate downloaded: %s", filePath)
	},
}

func init() {
	var filePath string
	authorityDownloadCmd.Flags().StringVarP(&filePath, "out", "o", "", "path where root certificate should be stored")

}

func promptForCertificate() string {
	return utils.PromptForRequiredInput("Path to root certificate (e.g., /path/to/root.crt, recommended extension: .crt): ")
}
