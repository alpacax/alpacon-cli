package iam

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new user",
	Long: `
	Create a new user in the Alpacon. 
	This command allows you to add a new user by specifying required user information such as username and other relevant details.
	The server generates the password and the command prints it once on stdout.
	`,
	Example: ` 
	alpacon user create
	`,
	Run: func(cmd *cobra.Command, args []string) {

		isSaaS, err := config.IsSaaS()
		if err != nil {
			utils.CliErrorWithCode(utils.ExitCodeGeneralError, utils.ErrorCodeNotLoggedIn, "Not logged in. Run 'alpacon login' first.")
		}
		if isSaaS {
			utils.CliErrorWithExit("This command is only available for self-hosted workspaces. Use 'alpacon user invite' instead.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := alpaconClient.LoadCurrentUser(); err != nil {
			utils.CliErrorWithExit("Failed to load current user: %s", err)
		}

		if alpaconClient.Privileges == "general" {
			utils.CliErrorWithExit("Insufficient permissions to create users. This action requires staff or superuser privileges. Please contact your administrator to request elevated permissions")
		}

		userRequest := promptForUser(alpaconClient)

		password, err := iam.CreateUser(alpaconClient, userRequest)
		if err != nil {
			utils.CliErrorWithExit("Failed to create the new user: %s", err)
		}

		utils.CliSuccess("User created: %s", userRequest.Username)
		if err := printGeneratedPassword(os.Stdout, utils.CliInfo, password); err != nil {
			utils.CliErrorWithExit("The user was created, but %s. Set a new password for the user before they can log in.", err)
		}
	},
}

// Callers must call LoadCurrentUser first; ac.Privileges determines which fields are presented.
func promptForUser(ac *client.AlpaconClient) iam.UserCreateRequest {
	var userRequest iam.UserCreateRequest

	userRequest.Username = utils.PromptForRequiredInput("Username(required): ")
	userRequest.FirstName = utils.PromptForRequiredInput("First name(required): ")
	userRequest.LastName = utils.PromptForRequiredInput("Last name(required): ")
	userRequest.Email = utils.PromptForRequiredInput("Email(required): ")
	userRequest.Phone = utils.PromptForInput("Phone number(optional): ")
	userRequest.Tags = utils.PromptForInput("Tags (optional, comma-separated, e.g., #dev, #ops): ")
	userRequest.Description = utils.PromptForInput("Description(optional): ")
	userRequest.Shell = utils.PromptForInput("Shell(optional, An absolute path for a shell of choice. default: /bin/bash): ")

	userRequest.IsLdapUser = utils.PromptForBool("LDAP status")

	if ac.Privileges == "superuser" {
		userRequest.IsStaff = utils.PromptForBool("Staff status")
		userRequest.IsSuperuser = utils.PromptForBool("Superuser status")
	}

	return userRequest
}

// printGeneratedPassword keeps stdout to the password line alone so a script can capture it.
func printGeneratedPassword(stdout io.Writer, info func(string, ...any), password string) error {
	if password == "" {
		return errors.New("the server returned no password")
	}
	if _, altered := utils.SanitizeTerminalLine(password); altered {
		return errors.New("the generated password contains characters that cannot be shown safely")
	}
	info("This is the generated password. It is shown only once; pass it to the user securely.")
	_, err := fmt.Fprintln(stdout, password)
	return err
}
