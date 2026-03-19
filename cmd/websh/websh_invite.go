package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webshInviteCmd = &cobra.Command{
	Use:   "invite SESSION_ID",
	Short: "Invite users to a websh session by email",
	Long: `Invite one or more users to join an existing websh session.
An invitation email will be sent to each specified address.`,
	Example: `  alpacon websh invite abc123 --email user@example.com
  alpacon websh invite abc123 --email user1@example.com --email user2@example.com
  alpacon websh invite abc123 --email user@example.com --read-only`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sessionID := args[0]
		emails, _ := cmd.Flags().GetStringArray("email")
		readOnly, _ := cmd.Flags().GetBool("read-only")

		if len(emails) == 0 {
			utils.CliErrorWithExit("At least one --email is required.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = websh.InviteToSession(alpaconClient, sessionID, emails, readOnly)
		if err != nil {
			utils.CliErrorWithExit("Failed to invite users to websh session: %s.", err)
		}

		utils.CliSuccess("Invitations have been successfully sent to invitees.")
	},
}

func init() {
	webshInviteCmd.Flags().StringArray("email", []string{}, "Email address to invite (can be specified multiple times)")
	webshInviteCmd.Flags().Bool("read-only", false, "Invite as read-only viewer")
}
