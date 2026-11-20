package cert

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newCertDetailCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:     "describe CERT_ID",
		Aliases: []string{"desc"},
		Short:   "Show details of a certificate",
		Long: `
	The describe command fetches and displays detailed information about a specific certificate, 
	including its crt text, certificate authority and other relevant attributes. 
	`,
		Example: `
	alpacon cert describe 550e8400-e29b-41d4-a716-446655440000
	alpacon cert desc 550e8400-e29b-41d4-a716-446655440000
	`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			certId := args[0]

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			certDetail, err := cert.GetCertificateDetail(alpaconClient, certId)
			if err != nil {
				return utils.NewExitError("Failed to retrieve the certificate details: %s.", err)
			}

			return ios.PrintJson(certDetail)
		}),
	}
}
