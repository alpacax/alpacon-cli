package token

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newAclFileCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Manage file ACL rules for a token",
		Long: `Control which file paths an API token can access via cp.

Deny-by-default: if no file ACL exists for a token, all file transfers are denied.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newAclFileAddCmd(newClient, ios),
		newAclFileListCmd(newClient, ios),
		newAclFileDeleteCmd(newClient, ios),
	)
	return cmd
}
