package webhook

import (
	"github.com/alpacax/alpacon-cli/api/webhook"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webhookDeleteCmd = &cobra.Command{
	Use:     "delete WEBHOOK",
	Aliases: []string{"rm"},
	Short:   "Delete a webhook",
	Long: `
	This command permanently deletes a specified webhook from the Alpacon.
	The command requires the webhook name as its argument.
	`,
	Example: `
	alpacon webhook delete my-webhook
	alpacon webhook rm my-webhook
	alpacon webhook delete my-webhook -y
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		webhookName := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			if proceed, err := utils.Confirm("Delete webhook '%s'?", webhookName); err != nil || !proceed {
				return err
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = webhook.DeleteWebhook(alpaconClient, webhookName)
		if err != nil {
			return utils.NewExitError("Failed to delete the webhook: %s.", err)
		}

		utils.CliSuccess("Webhook deleted: %s", webhookName)
		return nil
	}),
}

func init() {
	webhookDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
