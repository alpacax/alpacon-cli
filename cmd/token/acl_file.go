package token

import (
	"errors"

	"github.com/spf13/cobra"
)

var aclFileCmd = &cobra.Command{
	Use:   "file",
	Short: "Manage file ACL rules for a token",
	Long: `Control which file paths an API token can access via cp.

Deny-by-default: if no file ACL exists for a token, all file transfers are denied.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = cmd.Help()
		return errors.New("a subcommand is required")
	},
}

func init() {
	aclFileCmd.AddCommand(aclFileAddCmd)
	aclFileCmd.AddCommand(aclFileListCmd)
	aclFileCmd.AddCommand(aclFileDeleteCmd)
}
