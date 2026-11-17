package iam

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func NewGroupCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Manage groups, members, and permissions",
		Long:  "Manage groups, role-based access controls, and group membership within the Alpacon workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newGroupListCmd(newClient, ios),
		newGroupDetailCmd(newClient, ios),
		newGroupDeleteCmd(newClient, ios),
		newGroupCreateCmd(newClient, ios),
		newGroupUpdateCmd(newClient, ios),
		newMemberCmd(newClient, ios),
	)
	return cmd
}
