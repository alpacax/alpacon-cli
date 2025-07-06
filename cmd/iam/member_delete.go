package iam

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var memberDeleteRequest iam.MemberDeleteRequest

var memberDeleteCmd = &cobra.Command{
	Use:     "delete",
	Aliases: []string{"rm"},
	Short:   "Remove a member from a group",
	Long: `
	This command removes an existing member from the specified group. 
	It's useful for managing group membership and ensuring only current members have access.
	`,
	Run: func(cmd *cobra.Command, args []string) {
		if memberRequest.Group == "" || memberRequest.User == "" || memberRequest.Role == "" {
			promptForDeleteMembers()
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = iam.DeleteMember(alpaconClient, memberDeleteRequest)
		if err != nil {
			utils.CliError("Failed to add the member to group: %s.", err)
		}

		utils.CliInfo("%s successfully deleted to %s.", memberRequest.User, memberRequest.Group)
	},
}

func init() {
	memberDeleteCmd.Flags().StringVarP(&memberDeleteRequest.Group, "group", "g", "", "Group")
	memberDeleteCmd.Flags().StringVarP(&memberDeleteRequest.User, "user", "u", "", "User")
}

func promptForDeleteMembers() {
	if memberDeleteRequest.Group == "" {
		memberDeleteRequest.Group = utils.PromptForRequiredInput("Group: ")
	}
	if memberDeleteRequest.User == "" {
		memberDeleteRequest.User = utils.PromptForRequiredInput("User: ")
	}
}
