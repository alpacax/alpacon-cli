package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var groupListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "all"},
	Short:   "Display a list of all groups",
	Long: `
	Display a detailed list of all groups registered in the Alpacon.
	This command provides information such as name, members, servers, and other relevant details.
	`,
	Example: `
	alpacon group ls
	alpacon groups
	alpacon group list
	alpacon group all
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		groupList, err := iam.GetGroupList(alpaconClient)
		if err != nil {
			utils.CliError("Failed to retrieve the group list: %s.", err)
		}

		utils.PrintTable(groupList)
	},
}
