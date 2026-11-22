package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newUserDeleteCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	var archiveHome bool
	cmd := &cobra.Command{
		Use:     "delete USER",
		Aliases: []string{"rm"},
		Short:   "Delete a specified user",
		Long: `
	Delete an Alpacon user account by exact username. Depending on the server and
	agent versions, the home directory is either kept in place or moved to
	/home/deleted_users/. Use --archive-home to request the move where the home
	directory is kept by default; servers that do not support the option ignore it
	and move the home directory anyway. Services that use files in the home
	directory may stop working after the move. Check the affected servers before
	reusing the username.
	`,
		Example: `
	alpacon user delete john
	alpacon user rm john
	alpacon user delete john --archive-home -y
	`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			userName := args[0]

			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				prompt := "Delete user '%s'?"
				if archiveHome {
					prompt = "Delete user '%s' and request archiving their home directory?"
				}
				if proceed, err := utils.Confirm(prompt, userName); err != nil || !proceed {
					return err
				}
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			if err := alpaconClient.LoadCurrentUser(); err != nil {
				return utils.NewExitError("Failed to load current user: %s", err)
			}

			if alpaconClient.Privileges == "general" {
				return utils.NewExitError("You do not have the permission to delete users.")
			}

			err = iam.DeleteUser(alpaconClient, userName, archiveHome)
			if err != nil {
				return utils.NewExitError("Failed to delete the user: %s", err)
			}

			ios.Success("User deleted: %s", userName)
			if archiveHome {
				ios.Warning("Home archiving was requested. Verify the move on the affected servers before reusing the username.")
			}
			return nil
		}),
	}
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&archiveHome, "archive-home", false, "Request moving the home directory to /home/deleted_users/ where it is kept by default")
	return cmd
}
