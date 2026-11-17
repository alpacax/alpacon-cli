package token

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewTokenCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens for CI/CD and automation",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newTokenCreateCmd(newClient, ios),
		newTokenListCmd(newClient, ios),
		newTokenDeleteCmd(newClient, ios),
		newTokenDuplicateCmd(newClient, ios),
		newTokenScopesCmd(newClient, ios),
		newAclCmd(newClient, ios),
	)
	return cmd
}
