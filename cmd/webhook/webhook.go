package webhook

import (
	"errors"
	"github.com/spf13/cobra"
)

var WebhookCmd = &cobra.Command{
	Use:     "webhook",
	Aliases: []string{"webhooks"},
	Short:   "Manage webhook configurations",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon webhook list', 'alpacon webhook create', 'alpacon webhook describe', 'alpacon webhook update', or 'alpacon webhook delete'. Run 'alpacon webhook --help' for more information")
	},
}

func init() {
	WebhookCmd.AddCommand(webhookListCmd)
	WebhookCmd.AddCommand(webhookDetailCmd)
	WebhookCmd.AddCommand(webhookCreateCmd)
	WebhookCmd.AddCommand(webhookUpdateCmd)
	WebhookCmd.AddCommand(webhookDeleteCmd)
}
