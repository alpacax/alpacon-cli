package agent

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewAgentCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage the Alpamon agent on a server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newUpgradeAgentCmd(newClient, ios),
		newRestartAgentCmd(newClient, ios),
		newInstallPAMCmd(newClient, ios),
	)
	return cmd
}
