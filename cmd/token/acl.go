package token

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newAclCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acl",
		Short: "Manage access control for API tokens",
		Long: `Configure fine-grained access control for API tokens.

Three independent ACL types enforce deny-by-default:
  command  — which shell commands the token can execute via websh/exec
  server   — which servers the token can access
  file     — which file paths the token can read/write via cp

If no ACL rule exists for a given type, that access is denied entirely.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newAclCommandCmd(newClient, ios),
		newAclServerCmd(newClient, ios),
		newAclFileCmd(newClient, ios),
		// Legacy top-level aliases (deprecated, hidden)
		newAclAddCmd(newClient, ios),
		newAclListCmd(newClient, ios),
		newAclDeleteCmd(newClient, ios),
	)
	return cmd
}
