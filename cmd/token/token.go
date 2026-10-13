package token

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var TokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage API tokens for CI/CD and automation",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
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
