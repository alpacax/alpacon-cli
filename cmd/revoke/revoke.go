package revoke

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewRevokeCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "revoke",
		Aliases: []string{"revoke-request"},
		Short:   "Manage certificate revocation requests",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newRevokeListCmd(newClient, ios),
		newRevokeDetailCmd(newClient, ios),
		newRevokeCreateCmd(newClient, ios),
		newRevokeApproveCmd(newClient, ios),
		newRevokeDenyCmd(newClient, ios),
		newRevokeRetryCmd(newClient, ios),
		newRevokeCancelCmd(newClient, ios),
	)
	return cmd
}
