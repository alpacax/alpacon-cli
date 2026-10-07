package webhook

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var WebhookCmd = &cobra.Command{
	Use:     "webhook",
	Aliases: []string{"webhooks"},
	Short:   "Configure webhooks for event notifications",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	WebhookCmd.AddCommand(webhookListCmd)
	WebhookCmd.AddCommand(webhookDetailCmd)
	WebhookCmd.AddCommand(webhookCreateCmd)
	WebhookCmd.AddCommand(webhookUpdateCmd)
	WebhookCmd.AddCommand(webhookDeleteCmd)
}
