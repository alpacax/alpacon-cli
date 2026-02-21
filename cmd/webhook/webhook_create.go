package webhook

import (
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/webhook"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webhookCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new webhook",
	Long: `
	Create a new webhook configuration. You will be prompted for the webhook name,
	URL, and owner if not provided via flags.
	`,
	Example: `
	alpacon webhook create
	alpacon webhook create --name=my-webhook --url=https://example.com/hook --owner=admin
	`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		url, _ := cmd.Flags().GetString("url")
		owner, _ := cmd.Flags().GetString("owner")
		sslVerify, _ := cmd.Flags().GetBool("ssl-verify")
		enabled, _ := cmd.Flags().GetBool("enabled")

		if name == "" {
			name = utils.PromptForRequiredInput("Webhook name: ")
		}
		if url == "" {
			url = utils.PromptForRequiredInput("Webhook URL: ")
		}
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if owner == "" {
			owner = alpaconClient.Username
		}

		ownerID, err := iam.GetUserIDByName(alpaconClient, owner)
		if err != nil {
			utils.CliErrorWithExit("Failed to find user: %s.", err)
		}

		request := webhook.WebhookCreateRequest{
			Name:      name,
			URL:       url,
			SSLVerify: sslVerify,
			Enabled:   enabled,
			Owner:     ownerID,
		}

		err = webhook.CreateWebhook(alpaconClient, request)
		if err != nil {
			utils.CliErrorWithExit("Failed to create webhook: %s.", err)
		}

		utils.CliSuccess("Webhook created: %s", name)
	},
}

func init() {
	var name, url, owner string
	var sslVerify, enabled bool
	webhookCreateCmd.Flags().StringVar(&name, "name", "", "Webhook name")
	webhookCreateCmd.Flags().StringVar(&url, "url", "", "Webhook URL")
	webhookCreateCmd.Flags().StringVar(&owner, "owner", "", "Owner username")
	webhookCreateCmd.Flags().BoolVar(&sslVerify, "ssl-verify", true, "Enable SSL verification")
	webhookCreateCmd.Flags().BoolVar(&enabled, "enabled", true, "Enable the webhook")
}
