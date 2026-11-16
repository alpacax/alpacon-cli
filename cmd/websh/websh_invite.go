package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newInviteCommand(newClient clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite SESSION_ID",
		Short: "Invite users to a Websh session by email",
		Long: `Invite one or more users to join an existing Websh session.
An invitation email will be sent to each specified address.`,
		Example: `  alpacon websh invite 550e8400-e29b-41d4-a716-446655440000 --email user@example.com
  alpacon websh invite 550e8400-e29b-41d4-a716-446655440000 --email user1@example.com --email user2@example.com
  alpacon websh invite 550e8400-e29b-41d4-a716-446655440000 --email user@example.com --read-only`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			emails, _ := cmd.Flags().GetStringArray("email")
			readOnly, _ := cmd.Flags().GetBool("read-only")

			if len(emails) == 0 {
				return utils.NewUsageExitError("At least one --email is required.")
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			err = websh.InviteToSession(alpaconClient, sessionID, emails, readOnly)
			if err != nil {
				return utils.NewExitError("Failed to invite users to websh session: %s.", utils.InteractiveOnly(err, "invite users to a Websh session"))
			}

			utils.CliSuccess("Invitations have been successfully sent to invitees.")
			return nil
		}),
	}
	cmd.Flags().StringArray("email", []string{}, "Email address to invite (can be specified multiple times)")
	cmd.Flags().Bool("read-only", false, "Invite as read-only viewer")
	return cmd
}
