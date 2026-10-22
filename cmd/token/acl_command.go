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
'delete' are refused with command_acl_moved; a Command ACL admin issues or revokes
the entry under Pre-approved commands on the service token's page in the web
console, and a personal API token has no command list on the ledger. 'ls' is
unaffected.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	aclCommandCmd.AddCommand(aclCommandAddCmd)
	aclCommandCmd.AddCommand(aclCommandListCmd)
	aclCommandCmd.AddCommand(aclCommandDeleteCmd)
}
