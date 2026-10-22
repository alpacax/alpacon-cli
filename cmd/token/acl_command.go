package token

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclCommandCmd = &cobra.Command{
	Use:   "command",
	Short: "Manage command ACL rules for a token",
	Long: `Configure which server-side shell commands an API token is allowed to execute
via websh or exec (e.g., "whoami", "systemctl status *", "docker compose *").

On servers that manage command ACLs on the command pre-approval ledger, 'add' and
'delete' are refused with command_acl_moved; a ledger admin issues or revokes the
entry on the service token's command ACL card in the web console, and a personal
API token has no command list on this path. 'ls' works as before.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	aclCommandCmd.AddCommand(aclCommandAddCmd)
	aclCommandCmd.AddCommand(aclCommandListCmd)
	aclCommandCmd.AddCommand(aclCommandDeleteCmd)
}
