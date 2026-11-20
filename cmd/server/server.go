package server

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewServerCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "server",
		Aliases: []string{"servers"},
		Short:   "Manage registered servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newServerListCmd(newClient, ios),
		newServerDetailCmd(newClient, ios),
		newServerCreateCmd(newClient, ios),
		newServerDeleteCmd(newClient, ios),
		newServerUpdateCmd(newClient, ios),
		newTokenCmd(newClient, ios),
		newServerRefreshCmd(newClient, ios),
	)
	return cmd
}
