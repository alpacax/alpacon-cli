package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var groupUpdateCmd = &cobra.Command{
	Use:   "update [GROUP NAME]",
	Short: "Update the group information",
	Long: `
	Update the group information in the Alpacon.
	This command opens your editor with the current group data, allowing you to modify fields such as
	display name, tags, servers, and other configuration. Not all fields may be editable depending on your permissions.
	After saving, the updated group information is displayed for verification.
	`,
	Example: `
	alpacon group update my-group
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		groupName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		groupDetail, err := iam.UpdateGroup(alpaconClient, groupName)
		if err != nil {
			utils.CliErrorWithExit("Failed to update the group info: %s.", err)
		}

		utils.CliSuccess("Group updated: %s", groupName)
		utils.PrintJson(groupDetail)
	},
}
