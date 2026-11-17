package cert

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var certDownloadCmd = newCertDownloadCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newCertDownloadCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download CERT_ID",
		Short: "Download a certificate",
		Long: `
	Download a certificate from the server and save it to a specified file path. 
	The path argument should include the file name and extension where the certificate will be stored. 
	For example, '/path/to/certificate.crt'. The recommended file extension for certificates is '.crt'.`,
		Example: `
	alpacon cert download 550e8400-e29b-41d4-a716-446655440000 --out=/path/to/certificate.crt
	`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			certId := args[0]
			filePath, _ := cmd.Flags().GetString("out")
			if filePath == "" {
				var err error
				if filePath, err = promptForCertificate(); err != nil {
					return err
				}
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			err = cert.DownloadCertificate(alpaconClient, certId, filePath)
			if err != nil {
				return utils.NewExitError("Failed to download the certificate from authority: %s.", err)
			}

			ios.Success("Certificate downloaded: %s", filePath)
			return nil
		}),
	}
	var filePath string
	cmd.Flags().StringVarP(&filePath, "out", "o", "", "path where certificate should be stored")
	return cmd
}

func promptForCertificate() (string, error) {
	return utils.ReadRequiredInput("Path to certificate (e.g., /path/to/certificate.crt, recommended extension: .crt): ")
}
