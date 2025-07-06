package iam

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var memberRequest iam.MemberAddRequest

var memberAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a member to a group with a specific role",
	Long: `
	This command adds a new member to the specified group and assigns a role to them. 
	It's used for managing group memberships and roles within the group.
	`,
	Run: func(cmd *cobra.Command, args []string) {
		if memberRequest.Group == "" || memberRequest.User == "" || memberRequest.Role == "" {
			promptForMembers()
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = iam.AddMember(alpaconClient, memberRequest)
		if err != nil {
			utils.CliError("Failed to add the member to group: %s.", err)
		}

		utils.CliInfo("%s successfully added to %s.", memberRequest.User, memberRequest.Group)
	},
}

func init() {
	memberAddCmd.Flags().StringVarP(&memberRequest.Group, "group", "g", "", "Group")
	memberAddCmd.Flags().StringVarP(&memberRequest.User, "user", "u", "", "User")
	memberAddCmd.Flags().StringVarP(&memberRequest.Group, "role", "r", "", "Role of member")
}

func promptForMembers() {
	if memberRequest.Group == "" {
		memberRequest.Group = utils.PromptForRequiredInput("Group: ")
	}
	if memberRequest.User == "" {
		memberRequest.User = utils.PromptForRequiredInput("User: ")
	}
	if memberRequest.Role == "" {
		memberRequest.Role = promptForRole()
	}
}

func promptForRole() string {
	for {
		role := utils.PromptForRequiredInput("Role(owner, manager, member): ")
		if strings.ToLower(role) == "owner" || strings.ToLower(role) == "manager" || strings.ToLower(role) == "member" {
			return role
		}
		fmt.Println("Invalid role. Please choose 'owner', 'manager', or 'member'.")
	}
}
