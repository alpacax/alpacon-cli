package token

import (
	"errors"
	"github.com/spf13/cobra"
)

var TokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Commands to manage api tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon token create', 'alpacon token list', 'alpacon token delete', or 'alpacon token acl' to manage API tokens. Run 'alpacon token --help' for more information")
	},
}

func init() {
	TokenCmd.AddCommand(tokenCreateCmd)
	TokenCmd.AddCommand(tokenListCmd)
	TokenCmd.AddCommand(tokenDeleteCmd)

	// ACL
	TokenCmd.AddCommand(AclCmd)
}
