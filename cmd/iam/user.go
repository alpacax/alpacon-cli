package iam

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewUserCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "List, create, describe, update, and delete users",
		Long:  "Manage user accounts and their permissions within the Alpacon workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newUserListCmd(newClient, ios),
		newUserDetailCmd(newClient, ios),
		newUserDeleteCmd(newClient, ios),
		newUserCreateCmd(newClient, ios),
		newUserUpdateCmd(newClient, ios),
		newUserInviteCmd(newClient, ios),
		newUserRoleCmd(newClient, ios),
		newUserPermissionCmd(newClient, ios),
	)
	return cmd
}
