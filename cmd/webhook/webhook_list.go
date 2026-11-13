package webhook

import (
	"github.com/alpacax/alpacon-cli/api/webhook"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webhookListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List all webhooks",
	Long: `
	List all configured webhooks with their name, URL, and status.
	`,
	Example: `
	alpacon webhook list
	alpacon webhook ls
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		webhookList, err := webhook.GetWebhookList(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to get webhooks: %s.", err)
		}

		return utils.PrintTable(webhookList)
	}),
}
