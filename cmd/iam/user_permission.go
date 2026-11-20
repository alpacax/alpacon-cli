package iam

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newUserPermissionCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "permission",
		Aliases: []string{"permissions"},
		Short:   "Inspect what a user is allowed to do",
		Long: `Inspect the permissions a user's roles add up to.

Role membership and effective access are different questions. 'alpacon user role
ls' answers which roles someone holds; this answers what those roles let them do,
and where each one came from.`,
		Example: `  alpacon user permission ls john
  alpacon user permission can-i john server:update`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newUserPermissionListCmd(newClient, ios),
		newUserPermissionCanICmd(newClient, ios),
	)
	return cmd
}
