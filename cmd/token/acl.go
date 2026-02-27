package token

import (
	"errors"
	"github.com/spf13/cobra"
)

var AclCmd = &cobra.Command{
	Use:   "acl",
	Short: "Manage command access for API tokens",
	Long: `Configure access control for API tokens, specifying which commands each token can execute.

ACL rules apply to both CLI commands (e.g., "server ls", "websh") and server-side shell
commands executed via websh or exec (e.g., "whoami", "systemctl status *").

Create, list, and modify ACL rules to fine-tune command execution permissions.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon token acl list', 'alpacon token acl add', or 'alpacon token acl delete' to manage access control rules. Run 'alpacon token acl --help' for more information")
	},
}

func init() {
	AclCmd.AddCommand(aclListCmd)
	AclCmd.AddCommand(aclAddCmd)
	AclCmd.AddCommand(aclDeleteCmd)
}
