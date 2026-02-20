package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userDeleteCmd = &cobra.Command{
	Use:     "delete [USER NAME]",
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
	Run: func(cmd *cobra.Command, args []string) {
		userName := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete user '%s'?", userName)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if alpaconClient.Privileges == "general" {
			utils.CliErrorWithExit("You do not have the permission to delete users.")
		}

		err = iam.DeleteUser(alpaconClient, userName)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the user: %s.", err)
		}

		utils.CliSuccess("User deleted: %s", userName)
	},
}

func init() {
	userDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
