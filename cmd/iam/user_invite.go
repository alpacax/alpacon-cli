package iam

import (
	"strings"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		isSaaS, err := config.IsSaaS()
		if err != nil {
			return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
		}
		if !isSaaS {
			return utils.NewExitError("This command is only available on Alpacon Cloud workspaces.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := alpaconClient.LoadCurrentUser(); err != nil {
			return utils.NewExitError("Failed to load current user: %s", err)
		}

		if alpaconClient.Privileges == "general" {
			return utils.NewExitError("Insufficient permissions to invite users. This action requires staff or superuser privileges. Please contact your administrator to request elevated permissions")
		}

		var email string
		if len(args) > 0 {
			email = args[0]
		} else {
			if !utils.IsInteractiveShell() {
				return utils.NewUsageExitError("email argument is required in non-interactive mode")
			}
			if email, err = utils.ReadRequiredInput("Email: "); err != nil {
				return err
			}
		}

		if !strings.Contains(email, "@") {
			if len(args) > 0 {
				return utils.NewUsageExitError("invalid email address: %s", email)
			}
			return utils.NewExitError("invalid email address: %s", email)
		}

		err = iam.InviteUser(alpaconClient, iam.UserInviteRequest{Email: email})
		if err != nil {
			return utils.NewExitError("Failed to invite user: %s", err)
		}

		utils.CliSuccess("Invitation sent to %s", email)
		return nil
	}),
}
