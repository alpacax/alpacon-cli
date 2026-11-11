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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {

		isSaaS, err := config.IsSaaS()
		if err != nil {
			return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
		}
		if isSaaS {
			return utils.NewExitError("This command is only available for self-hosted workspaces. Use 'alpacon user invite' instead.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := alpaconClient.LoadCurrentUser(); err != nil {
			return utils.NewExitError("Failed to load current user: %s", err)
		}

		if alpaconClient.Privileges == "general" {
			return utils.NewExitError("Insufficient permissions to create users. This action requires staff or superuser privileges. Please contact your administrator to request elevated permissions")
		}

		userRequest, err := promptForUser(alpaconClient)
		if err != nil {
			return err
		}

		password, err := iam.CreateUser(alpaconClient, userRequest)
		if err != nil {
			return utils.NewExitError("Failed to create the new user: %s", err)
		}

		utils.CliSuccess("User created: %s", userRequest.Username)
		if err := printGeneratedPassword(os.Stdout, utils.CliInfo, password); err != nil {
			return utils.NewExitError("The user was created, but %s. Set a new password for the user before they can log in.", err)
		}
		return nil
	}),
}

// promptForUser offers the privilege fields only to a superuser, so the caller must run
// LoadCurrentUser first.
func promptForUser(ac *client.AlpaconClient) (iam.UserCreateRequest, error) {
	var userRequest iam.UserCreateRequest

	fields := []struct {
		value    *string
		prompt   string
		required bool
	}{
		{&userRequest.Username, "Username(required): ", true},
		{&userRequest.FirstName, "First name(required): ", true},
		{&userRequest.LastName, "Last name(required): ", true},
		{&userRequest.Email, "Email(required): ", true},
		{&userRequest.Phone, "Phone number(optional): ", false},
		{&userRequest.Tags, "Tags (optional, comma-separated, e.g., #dev, #ops): ", false},
		{&userRequest.Description, "Description(optional): ", false},
		{&userRequest.Shell, "Shell(optional, An absolute path for a shell of choice. default: /bin/bash): ", false},
	}
	for _, f := range fields {
		read := utils.ReadInput
		if f.required {
			read = utils.ReadRequiredInput
		}
		input, err := read(f.prompt)
		if err != nil {
			return userRequest, err
		}
		*f.value = input
	}

	userRequest.IsLdapUser = utils.PromptForBool("LDAP status")

	if ac.Privileges == "superuser" {
		userRequest.IsStaff = utils.PromptForBool("Staff status")
		userRequest.IsSuperuser = utils.PromptForBool("Superuser status")
	}

	return userRequest, nil
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
