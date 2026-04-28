package token

import (
	"errors"

	"github.com/spf13/cobra"
)

var AclCmd = &cobra.Command{
	Use:   "acl",
	Short: "Manage access control for API tokens",
	Long: `Configure fine-grained access control for API tokens.

Three independent ACL types enforce deny-by-default:
  command  — which shell commands the token can execute via websh/exec
  server   — which servers the token can access
  file     — which file paths the token can read/write via cp

If no ACL rule exists for a given type, that access is denied entirely.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = cmd.Help()
		return errors.New("a subcommand is required. Run 'alpacon token acl --help' for more information")
	},
}

func init() {
	AclCmd.AddCommand(aclCommandCmd)
	AclCmd.AddCommand(aclServerCmd)
	AclCmd.AddCommand(aclFileCmd)

	// Legacy top-level aliases (deprecated, hidden)
	AclCmd.AddCommand(aclAddCmd)
	AclCmd.AddCommand(aclListCmd)
	AclCmd.AddCommand(aclDeleteCmd)
}
