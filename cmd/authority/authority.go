package authority

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewAuthorityCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "authority",
		Short: "Manage private certificate authorities",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newAuthorityCreateCmd(newClient, ios),
		newAuthorityListCmd(newClient, ios),
		newAuthorityDetailCmd(newClient, ios),
		newAuthorityDownloadCmd(newClient, ios),
		newAuthorityDeleteCmd(newClient, ios),
		newAuthorityUpdateCmd(newClient, ios),
		newAuthorityDownloadCrlCmd(newClient, ios),
	)
	return cmd
}
