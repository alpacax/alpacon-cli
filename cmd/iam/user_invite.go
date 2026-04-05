package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userInviteCmd = &cobra.Command{
	Use:   "invite EMAIL",
	Short: "Invite a user to the workspace",
	Long:  "Invite a user to the workspace by email. This command is available only in Auth0 environments and requires staff or superuser privileges. The invitee will receive an email with a link to join the workspace.",
	Example: `  alpacon user invite user@example.com`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if alpaconClient.AccessToken == "" {
			utils.CliErrorWithExit("user invite requires Auth0 authentication. Please log in with Auth0 first.")
		}

		if alpaconClient.Privileges == "general" {
			utils.CliErrorWithExit("Insufficient permissions to invite users. This action requires staff or superuser privileges. Please contact your administrator to request elevated permissions")
		}

		email := args[0]
		err = iam.InviteUser(alpaconClient, iam.UserInviteRequest{Email: email})
		if err != nil {
			utils.CliErrorWithExit("Failed to invite user: %s", err)
		}

		utils.CliSuccess("Invitation sent to %s", email)
	},
}
