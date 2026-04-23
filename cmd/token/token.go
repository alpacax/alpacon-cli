package token

import (
	"errors"
	"github.com/spf13/cobra"
)

var TokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage API tokens for CI/CD and automation",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon token create', 'alpacon token list', 'alpacon token delete', 'alpacon token duplicate', 'alpacon token acl', or 'alpacon token scopes' to manage API tokens. Run 'alpacon token --help' for more information")
	},
}

func init() {
	TokenCmd.AddCommand(tokenCreateCmd)
	TokenCmd.AddCommand(tokenListCmd)
	TokenCmd.AddCommand(tokenDeleteCmd)
	TokenCmd.AddCommand(tokenDuplicateCmd)
	TokenCmd.AddCommand(tokenScopesCmd)

	// ACL
	TokenCmd.AddCommand(AclCmd)
}
