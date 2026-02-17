package revoke

import (
	"errors"
	"github.com/spf13/cobra"
)

var RevokeCmd = &cobra.Command{
	Use:     "revoke",
	Aliases: []string{"revoke-request"},
	Short:   "Manage certificate revoke requests",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon revoke list', 'alpacon revoke create', 'alpacon revoke describe', 'alpacon revoke approve', 'alpacon revoke deny', 'alpacon revoke retry', or 'alpacon revoke cancel'. Run 'alpacon revoke --help' for more information")
	},
}

func init() {
	RevokeCmd.AddCommand(revokeListCmd)
	RevokeCmd.AddCommand(revokeDetailCmd)
	RevokeCmd.AddCommand(revokeCreateCmd)
	RevokeCmd.AddCommand(revokeApproveCmd)
	RevokeCmd.AddCommand(revokeDenyCmd)
	RevokeCmd.AddCommand(revokeRetryCmd)
	RevokeCmd.AddCommand(revokeCancelCmd)
}
