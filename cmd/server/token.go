package server

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newTokenCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage server registration tokens",
		Long:  "Create, list, and delete registration tokens used by servers to self-register via Alpamon.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newTokenCreateCmd(newClient, ios),
		newTokenListCmd(newClient, ios),
		newTokenDeleteCmd(newClient, ios),
	)
	return cmd
}
