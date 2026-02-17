package webhook

import (
	"github.com/alpacax/alpacon-cli/api/webhook"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webhookUpdateCmd = &cobra.Command{
	Use:   "update [WEBHOOK NAME]",
	Short: "Update a webhook configuration",
	Long: `
	Update an existing webhook configuration in the Alpacon.
	This command opens your editor with the current webhook data, allowing you to modify fields such as
	name, URL, SSL verification, and enabled status. After saving, the updated webhook information
	is displayed for verification.
	`,
	Example: `
	alpacon webhook update my-webhook
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		webhookName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		webhookDetail, err := webhook.UpdateWebhook(alpaconClient, webhookName)
		if err != nil {
			utils.CliErrorWithExit("Failed to update the webhook: %s.", err)
		}

		utils.CliSuccess("Webhook updated: %s", webhookName)
		utils.PrintJson(webhookDetail)
	},
}
