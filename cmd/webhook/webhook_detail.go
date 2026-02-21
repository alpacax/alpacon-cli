package webhook

import (
	"github.com/alpacax/alpacon-cli/api/webhook"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webhookDetailCmd = &cobra.Command{
	Use:     "describe WEBHOOK",
	Aliases: []string{"desc"},
	Short:   "Display detailed information about a specific webhook",
	Long: `
	The describe command fetches and displays detailed information about a specific webhook,
	including its URL, SSL verification setting, enabled status, and owner.
	`,
	Example: `
	alpacon webhook describe my-webhook
	alpacon webhook desc my-webhook
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		webhookName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		webhookID, err := webhook.GetWebhookIDByName(alpaconClient, webhookName)
		if err != nil {
			utils.CliErrorWithExit("Failed to find the webhook: %s.", err)
		}

		webhookDetail, err := webhook.GetWebhookDetail(alpaconClient, webhookID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the webhook details: %s.", err)
		}

		utils.PrintJson(webhookDetail)
	},
}
