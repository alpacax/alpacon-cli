package webhook

import (
	"github.com/alpacax/alpacon-cli/api/webhook"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webhookListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "all"},
	Short:   "List all webhooks",
	Long: `
	List all configured webhooks with their name, URL, status, and owner information.
	`,
	Example: `
	alpacon webhook list
	alpacon webhook ls
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		webhookList, err := webhook.GetWebhookList(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to get webhooks: %s.", err)
		}

		utils.PrintTable(webhookList)
	},
}
