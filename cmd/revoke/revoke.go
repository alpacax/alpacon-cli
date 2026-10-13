package revoke

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var RevokeCmd = &cobra.Command{
	Use:     "revoke",
	Aliases: []string{"revoke-request"},
	Short:   "Manage certificate revocation requests",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
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
