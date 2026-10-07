package username

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// opGet is the operation identifier carried in JSON error envelopes (context.operation).
const opGet = "get"

var UsernameCmd = &cobra.Command{
	Use:   "username",
	Short: "Manage the username for your account's server access",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	UsernameCmd.AddCommand(usernameGetCmd)
	UsernameCmd.AddCommand(usernameSetCmd)
}
