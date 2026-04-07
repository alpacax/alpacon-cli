package iam

import (
	"strings"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userInviteCmd = &cobra.Command{
	Use:   "invite [EMAIL]",
	Short: "Invite a user to the workspace",
	Long: `Invite a user to the workspace by email. The invitee will receive an
email with a link to join the workspace.

This command requires staff or superuser privileges.`,
	Example: `  # Invite a user directly
  alpacon user invite user@example.com

  # Invite a user interactively
  alpacon user invite`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if alpaconClient.AccessToken == "" {
			utils.CliErrorWithExit("user invite is only available for Alpacon Cloud workspaces")
		}

		if alpaconClient.Privileges == "general" {
			utils.CliErrorWithExit("Insufficient permissions to invite users. This action requires staff or superuser privileges. Please contact your administrator to request elevated permissions")
		}

		var email string
		if len(args) > 0 {
			email = args[0]
		} else {
			if !utils.IsInteractiveShell() {
				utils.CliErrorWithExit("email argument is required in non-interactive mode")
			}
			email = utils.PromptForRequiredInput("Email: ")
		}

		if !strings.Contains(email, "@") {
			utils.CliErrorWithExit("invalid email address: %s", email)
		}

		err = iam.InviteUser(alpaconClient, iam.UserInviteRequest{Email: email})
		if err != nil {
			utils.CliErrorWithExit("Failed to invite user: %s", err)
		}

		utils.CliSuccess("Invitation sent to %s", email)
	},
}
