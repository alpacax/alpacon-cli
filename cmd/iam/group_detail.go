package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var groupDetailCmd = &cobra.Command{
	Use:     "describe [GROUP NAME]",
	Aliases: []string{"desc"},
	Short:   "Display detailed information about a specific group",
	Long: `
	The describe command fetches and displays detailed information about a specific group, 
	including its description, member names and other relevant attributes. 
	`,
	Example: ` 
	# Display details of a group named 'alpacon'
  	alpacon group describe alpacon
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		groupName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		groupId, err := iam.GetGroupIDByName(alpaconClient, groupName)
		if err != nil {
			utils.CliError("Failed to retrieve the group details: %s. Please check if the groupname is correct and try again.", err)
		}
		groupDetail, err := iam.GetGroupDetail(alpaconClient, groupId)
		if err != nil {
			utils.CliError("Failed to retrieve the group details: %s.", err)
		}

		utils.PrintJson(groupDetail)
	},
}
