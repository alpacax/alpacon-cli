package token

import (
	"errors"

	"github.com/spf13/cobra"
)

var aclCommandCmd = &cobra.Command{
	Use:   "command",
	Short: "Manage command ACL rules for a token",
	Long: `Configure which server-side shell commands an API token is allowed to execute
via websh or exec (e.g., "whoami", "systemctl status *", "docker compose *").`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon token acl command add', 'alpacon token acl command ls', or 'alpacon token acl command delete'. Run 'alpacon token acl command --help' for more information")
	},
}

func init() {
	aclCommandCmd.AddCommand(aclCommandAddCmd)
	aclCommandCmd.AddCommand(aclCommandListCmd)
	aclCommandCmd.AddCommand(aclCommandDeleteCmd)
}
