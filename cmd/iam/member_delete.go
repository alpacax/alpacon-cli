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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		if memberDeleteRequest.Group == "" || memberDeleteRequest.User == "" {
			promptForDeleteMembers()
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Remove member '%s' from group '%s'?", memberDeleteRequest.User, memberDeleteRequest.Group)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = iam.DeleteMember(alpaconClient, memberDeleteRequest)
		if err != nil {
			return utils.NewExitError("Failed to remove the member from group: %s.", err)
		}

		utils.CliSuccess("Member %s removed from group %s", memberDeleteRequest.User, memberDeleteRequest.Group)
		return nil
	}),
}

func init() {
	memberDeleteCmd.Flags().StringVarP(&memberDeleteRequest.Group, "group", "g", "", "Group")
	memberDeleteCmd.Flags().StringVarP(&memberDeleteRequest.User, "user", "u", "", "User")
	memberDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}

func promptForDeleteMembers() {
	if memberDeleteRequest.Group == "" {
		memberDeleteRequest.Group = utils.PromptForRequiredInput("Group: ")
	}
	if memberDeleteRequest.User == "" {
		memberDeleteRequest.User = utils.PromptForRequiredInput("User: ")
	}
}
