package iam

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new user",
	Long: `
	Create a new user in the Alpacon. 
	This command allows you to add a new user by specifying required user information such as username, password, and other relevant details. 
	`,
	Example: ` 
	alpacon user create
	`,
	Run: func(cmd *cobra.Command, args []string) {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if alpaconClient.Privileges == "general" {
			utils.CliError("Insufficient permissions to create users. This action requires staff or superuser privileges. Please contact your administrator to request elevated permissions")
		}

		userRequest := promptForUser(alpaconClient)

		err = iam.CreateUser(alpaconClient, userRequest)
		if err != nil {
			utils.CliError("Failed to create the new user: %s.", err)
		}

		utils.CliInfo("%s user successfully created to alpacon.", userRequest.Username)
	},
}

func promptForUser(ac *client.AlpaconClient) iam.UserCreateRequest {
	var userRequest iam.UserCreateRequest

	userRequest.Username = utils.PromptForRequiredInput("Username(required): ")
	for {
		password := utils.PromptForPassword("Password(required): ")
		confirmPassword := utils.PromptForPassword("Confirm Password: ")

		if password == confirmPassword {
			userRequest.Password = password
			break
		} else {
			fmt.Println("Passwords do not match. Please try again.")
		}
	}
	userRequest.FirstName = utils.PromptForRequiredInput("First name(required): ")
	userRequest.LastName = utils.PromptForRequiredInput("Last name(required): ")
	userRequest.Email = utils.PromptForRequiredInput("Email(required): ")
	userRequest.Phone = utils.PromptForInput("Phone number(optional): ")
	userRequest.Tags = utils.PromptForInput("Tags(optional, Add tags for this user so that people can find easily. Tags should start with `#` and be comma-separated.): ")
	userRequest.Description = utils.PromptForInput("Description(optional): ")
	userRequest.Shell = utils.PromptForInput("Shell(optional, An absolute path for a shell of choice. default: /bin/bash): ")

	userRequest.IsLdapUser = utils.PromptForBool("LDAP status: ")

	if ac.Privileges == "superuser" {
		userRequest.IsStaff = utils.PromptForBool("Staff status:")
		userRequest.IsSuperuser = utils.PromptForBool("Superuser status:")
	}

	return userRequest
}
