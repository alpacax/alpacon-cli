package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityDownloadCrlCmd = newAuthorityDownloadCrlCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newAuthorityDownloadCrlCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download-crl AUTHORITY",
		Short: "Download a certificate revocation list (CRL)",
		Long: `
	Download the certificate revocation list (CRL) from the specified authority and save it to a file.
	The path argument should include the file name and extension where the CRL will be stored.
	For example, '/path/to/revoked.crl'. The recommended file extension is '.crl'.`,
		Example: `
	alpacon authority download-crl "Root CA" --out=/path/to/revoked.crl
	`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			authorityName := args[0]
			filePath, _ := cmd.Flags().GetString("out")
			if filePath == "" {
				var err error
				if filePath, err = promptForCRL(); err != nil {
					return err
				}
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			authorityID, err := cert.GetAuthorityIDByName(alpaconClient, authorityName)
			if err != nil {
				return utils.NewExitError("Failed to find authority: %s.", err)
			}

			err = cert.DownloadCRL(alpaconClient, authorityID, filePath)
			if err != nil {
				return utils.NewExitError("Failed to download the CRL from authority: %s.", err)
			}

			ios.Success("CRL downloaded: %s", filePath)
			return nil
		}),
	}
	var filePath string
	cmd.Flags().StringVarP(&filePath, "out", "o", "", "path where CRL should be stored")
	return cmd
}

func promptForCRL() (string, error) {
	return utils.ReadRequiredInput("Path to CRL file (e.g., /path/to/revoked.crl, recommended extension: .crl): ")
}
