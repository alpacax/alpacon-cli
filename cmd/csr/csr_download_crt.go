package csr

import (
	certApi "github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var csrDownloadCrtCmd = &cobra.Command{
	Use:   "download-crt CSR_ID",
	Short: "Download the certificate for a CSR",
	Long: `
	Download the signed certificate associated with a CSR.
	The CSR must be in 'signed' status for the certificate to be available.
	Use 'alpacon csr ls' to check the status of your CSRs.`,
	Example: `
	alpacon csr download-crt 550e8400-e29b-41d4-a716-446655440000 --out=/path/to/certificate.crt`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		csrId := args[0]
		filePath, _ := cmd.Flags().GetString("out")
		if filePath == "" {
			filePath = promptForCrtPath()
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = certApi.DownloadCertificateByCSR(alpaconClient, csrId, filePath)
		if err != nil {
			utils.CliErrorWithExit("Failed to download the certificate: %s.", err)
		}

		utils.CliSuccess("Certificate downloaded: %s", filePath)
	},
}

func init() {
	var filePath string
	csrDownloadCrtCmd.Flags().StringVarP(&filePath, "out", "o", "", "path where certificate should be stored")
}

func promptForCrtPath() string {
	return utils.PromptForRequiredInput("Path to certificate (e.g., /path/to/certificate.crt): ")
}
