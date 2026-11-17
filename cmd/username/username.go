package username

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

// opGet is the operation identifier carried in JSON error envelopes (context.operation).
const opGet = "get"

func NewUsernameCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "username",
		Short: "Manage your username for server access",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newUsernameGetCmd(newClient, ios),
		newUsernameSetCmd(newClient, ios),
	)
	return cmd
}
