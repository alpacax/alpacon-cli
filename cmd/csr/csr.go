package csr

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewCsrCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "csr",
		Short: "Manage certificate signing requests (CSRs)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newCsrCreateCmd(newClient, ios),
		newCsrListCmd(newClient, ios),
		newCsrApproveCmd(newClient, ios),
		newCsrDenyCmd(newClient, ios),
		newCsrDeleteCmd(newClient, ios),
		newCsrDetailCmd(newClient, ios),
		newCsrDownloadCrtCmd(newClient, ios),
		newCsrRetryCmd(newClient, ios),
	)
	return cmd
}
