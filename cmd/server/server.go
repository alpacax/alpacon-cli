package server

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

var ServerCmd = &cobra.Command{
	Use:     "server",
	Aliases: []string{"servers"},
	Short:   "Manage registered servers",
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
