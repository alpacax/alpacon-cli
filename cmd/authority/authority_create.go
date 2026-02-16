package authority

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new certificate authority",
	Long: `
  	Initializes a new certificate authority within the system, allowing you to sign and manage certificates and define their policies.
	`,
	Example: `alpacon authority create`,
	Run: func(cmd *cobra.Command, args []string) {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authorityRequest := promptForAuthority(alpaconClient)

		response, err := cert.CreateAuthority(alpaconClient, authorityRequest)
		if err != nil {
			utils.CliErrorWithExit("Failed to create the new authority: %s.", err)
		}

		installAuthorityInfo(response)
	},
}

func promptForAuthority(ac *client.AlpaconClient) cert.AuthorityRequest {
	var authorityRequest cert.AuthorityRequest

	authorityRequest.Name = utils.PromptForRequiredInput("Common name for the CA. (e.g., AlpacaX Root CA): ")
	authorityRequest.Organization = utils.PromptForRequiredInput("Organization name that this CA belongs to. (e.g., AlpacaX): ")
	authorityRequest.Domain = utils.PromptForRequiredInput("Domain name of the root certificate: ")
	authorityRequest.RootValidDays = utils.PromptForIntInput("Root certificate validity in days (default: 3650): ", 365*10)
	authorityRequest.DefaultValidDays = utils.PromptForIntInput("Child certificate validity in days (default: 365): ", 365)
	authorityRequest.MaxValidDays = utils.PromptForIntInput("Maximum valid days that users can request (default: 730): ", 365*2)

	agent := utils.PromptForRequiredInput("Name of server to run this CA on: ")
	agentID, err := server.GetServerIDByName(ac, agent)
	if err != nil {
		utils.CliErrorWithExit("Failed to retrieve the server %s", err)
	}
	authorityRequest.Agent = agentID

	owner := utils.PromptForRequiredInput("Owner(username): ")
	authorityRequest.Owner, err = iam.GetUserIDByName(ac, owner)
	if err != nil {
		utils.CliErrorWithExit("Failed to retrieve the user %s", err)
	}

	return authorityRequest
}

func installAuthorityInfo(response cert.AuthorityCreateResponse) {
	fmt.Fprintln(os.Stderr)
	utils.PrintHeader("Installation instruction")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, response.Instruction)
	utils.CliWarning("After leaving this page, you cannot obtain the script again for security.")
}
