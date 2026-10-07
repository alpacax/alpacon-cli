package iam

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var UserCmd = &cobra.Command{
	Use:   "user",
	Short: "List, create, describe, update, and delete users",
	Long:  "Manage user accounts and their permissions within the Alpacon workspace.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	UserCmd.AddCommand(userListCmd)
	UserCmd.AddCommand(userDetailCmd)
	UserCmd.AddCommand(userDeleteCmd)
	UserCmd.AddCommand(userCreateCmd)
	UserCmd.AddCommand(userUpdateCmd)
	UserCmd.AddCommand(userInviteCmd)

	UserCmd.AddCommand(userRoleCmd)
	UserCmd.AddCommand(userPermissionCmd)
}
