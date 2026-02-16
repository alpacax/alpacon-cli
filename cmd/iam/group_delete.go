package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var groupDeleteCmd = &cobra.Command{
	Use:     "delete [GROUP NAME]",
	Aliases: []string{"rm"},
	Short:   "Delete a specified group",
	Long: `
	This command is used to permanently delete a specified group from the Alpacon. 
	The command requires the exact username as an argument.
	NOTE : alpacon(Alpacon users) group cannot delete or update memberships
	`,
	Example: ` 
	alpacon group delete [GROUP NAME]
	alpacon group rm [GROUP NAME]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		groupName := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete group '%s'?", groupName)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if alpaconClient.Privileges == "general" {
			utils.CliErrorWithExit("You do not have the permission to delete groups.")
		}

		err = iam.DeleteGroup(alpaconClient, groupName)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the group: %s.", err)
		}

		utils.CliSuccess("Group deleted: %s", groupName)
	},
}

func init() {
	groupDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
