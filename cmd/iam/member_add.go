package iam

import (
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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		if memberRequest.Group == "" || memberRequest.User == "" || memberRequest.Role == "" {
			if err := promptForMembers(); err != nil {
				return err
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = iam.AddMember(alpaconClient, memberRequest)
		if err != nil {
			return utils.NewExitError("Failed to add the member to group: %s.", err)
		}

		utils.CliSuccess("Member %s added to group %s", memberRequest.User, memberRequest.Group)
		return nil
	}),
}

func init() {
	memberAddCmd.Flags().StringVarP(&memberRequest.Group, "group", "g", "", "Group")
	memberAddCmd.Flags().StringVarP(&memberRequest.User, "user", "u", "", "User")
	memberAddCmd.Flags().StringVarP(&memberRequest.Role, "role", "r", "", "Role of member within the group (owner, manager, member). Not an RBAC workspace role—for those see 'alpacon user role'")
}

func promptForMembers() error {
	var err error
	if memberRequest.Group == "" {
		if memberRequest.Group, err = utils.ReadRequiredInput("Group: "); err != nil {
			return err
		}
	}
	if memberRequest.User == "" {
		if memberRequest.User, err = utils.ReadRequiredInput("User: "); err != nil {
			return err
		}
	}
	if memberRequest.Role == "" {
		if memberRequest.Role, err = promptForRole(); err != nil {
			return err
		}
	}
	return nil
}

func promptForRole() (string, error) {
	for {
		role, err := utils.ReadRequiredInput("Role(owner, manager, member): ")
		if err != nil {
			return "", err
		}
		if strings.ToLower(role) == "owner" || strings.ToLower(role) == "manager" || strings.ToLower(role) == "member" {
			return role, nil
		}
		utils.CliWarning("Invalid role. Please choose 'owner', 'manager', or 'member'.")
	}
}
