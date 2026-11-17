package authority

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newAuthorityCreateCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:   "create",
		Short: "Create a new certificate authority",
		Long: `
  	Initializes a new certificate authority within the system, allowing you to sign and manage certificates and define their policies.

	CA creation requires an Alpacon API that accepts requests without an owner field.
	`,
		Example: `alpacon authority create`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			authorityRequest, err := promptForAuthority(alpaconClient)
			if err != nil {
				return err
			}

			response, err := cert.CreateAuthority(alpaconClient, authorityRequest)
			if err != nil {
				return utils.NewExitError("Failed to create the new authority: %s.", err)
			}

			installAuthorityInfo(ios, response)
			return nil
		}),
	}
}

func promptForAuthority(ac *client.AlpaconClient) (cert.AuthorityRequest, error) {
	var authorityRequest cert.AuthorityRequest
	var err error

	if authorityRequest.Name, err = utils.ReadRequiredInput("Common name for the CA. (e.g., AlpacaX Root CA): "); err != nil {
		return cert.AuthorityRequest{}, err
	}
	if authorityRequest.Organization, err = utils.ReadRequiredInput("Organization name that this CA belongs to. (e.g., AlpacaX): "); err != nil {
		return cert.AuthorityRequest{}, err
	}
	if authorityRequest.Domain, err = utils.ReadRequiredInput("Domain name of the root certificate: "); err != nil {
		return cert.AuthorityRequest{}, err
	}
	if authorityRequest.RootValidDays, err = utils.ReadIntInput("Root certificate validity in days (default: 3650): ", 365*10); err != nil {
		return cert.AuthorityRequest{}, err
	}
	if authorityRequest.DefaultValidDays, err = utils.ReadIntInput("Child certificate validity in days (default: 365): ", 365); err != nil {
		return cert.AuthorityRequest{}, err
	}
	if authorityRequest.MaxValidDays, err = utils.ReadIntInput("Maximum valid days that users can request (default: 730): ", 365*2); err != nil {
		return cert.AuthorityRequest{}, err
	}

	agent, err := utils.ReadRequiredInput("Name of server to run this CA on: ")
	if err != nil {
		return cert.AuthorityRequest{}, err
	}
	agentID, err := server.GetServerIDByName(ac, agent)
	if err != nil {
		return cert.AuthorityRequest{}, utils.NewExitError("Failed to retrieve the server %s", err)
	}
	authorityRequest.Agent = agentID

	return authorityRequest, nil
}

func installAuthorityInfo(ios *utils.IOStreams, response cert.AuthorityCreateResponse) {
	_, _ = fmt.Fprintln(ios.ErrOut)
	ios.PrintHeader("Installation instruction")
	_, _ = fmt.Fprintln(ios.ErrOut)
	_, _ = fmt.Fprintln(ios.ErrOut, response.Instruction)
	ios.Warning("After leaving this page, you cannot obtain the script again for security.")
}
