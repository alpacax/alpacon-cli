package token

import (
	"errors"

	"github.com/spf13/cobra"
)

var aclServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage server ACL rules for a token",
	Long: `Control which servers an API token can access.

Deny-by-default: if no server ACL exists for a token, access to all servers is denied.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = cmd.Help()
		return errors.New("a subcommand is required")
	},
}

func init() {
	aclServerCmd.AddCommand(aclServerAddCmd)
	aclServerCmd.AddCommand(aclServerListCmd)
	aclServerCmd.AddCommand(aclServerDeleteCmd)
}
