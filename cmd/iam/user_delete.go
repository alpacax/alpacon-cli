package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userDeleteCmd = &cobra.Command{
	Use:     "delete USER",
	Aliases: []string{"rm"},
	Short:   "Delete a specified user",
	Long: `
	This command is used to permanently delete a specified user account from the Alpacon. 
	The command requires the exact username as an argument.
	`,
	Example: `
	alpacon user delete john
	alpacon user rm john
	alpacon user delete john -y
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		userName := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			if proceed, err := utils.Confirm("Delete user '%s'?", userName); err != nil || !proceed {
				return err
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := alpaconClient.LoadCurrentUser(); err != nil {
			return utils.NewExitError("Failed to load current user: %s", err)
		}

		if alpaconClient.Privileges == "general" {
			return utils.NewExitError("You do not have the permission to delete users.")
		}

		err = iam.DeleteUser(alpaconClient, userName)
		if err != nil {
			return utils.NewExitError("Failed to delete the user: %s", err)
		}

		utils.CliSuccess("User deleted: %s", userName)
		return nil
	}),
}

func init() {
	userDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
