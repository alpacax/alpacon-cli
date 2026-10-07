package server

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var ServerCmd = &cobra.Command{
	Use:     "server",
	Aliases: []string{"servers"},
	Short:   "Manage registered servers",
	// NoArgs makes an unrecognized subcommand (a removed one included) fail as
	// "unknown command" instead of falling through to the help below.
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	ServerCmd.AddCommand(serverListCmd)
	ServerCmd.AddCommand(serverDetailCmd)
	ServerCmd.AddCommand(serverCreateCmd)
	ServerCmd.AddCommand(serverDeleteCmd)
	ServerCmd.AddCommand(serverUpdateCmd)
	ServerCmd.AddCommand(tokenCmd)
	ServerCmd.AddCommand(serverRefreshCmd)
}
