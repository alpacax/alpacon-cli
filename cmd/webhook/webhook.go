package webhook

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

func NewWebhookCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "webhook",
		Aliases: []string{"webhooks"},
		Short:   "Configure webhooks for event notifications",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newWebhookListCmd(newClient, ios),
		newWebhookDetailCmd(newClient, ios),
		newWebhookCreateCmd(newClient, ios),
		newWebhookUpdateCmd(newClient, ios),
		newWebhookDeleteCmd(newClient, ios),
	)
	return cmd
}
