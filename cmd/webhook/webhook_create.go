package webhook

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/iam"

	"github.com/alpacax/alpacon-cli/api/webhook"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webhookCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new webhook",
	Long: `Create a new webhook configuration.

You will be prompted for the webhook name, URL, provider, and SSL verification
if not provided via flags. Provider is auto-detected from the URL.
Owner defaults to the currently logged-in user.`,
	Example: `  alpacon webhook create
  alpacon webhook create --name=my-webhook --url=https://hooks.slack.com/services/xxx
  alpacon webhook create --name=my-webhook --url=https://example.com/hook --provider=custom
  alpacon webhook create --name=my-webhook --url=https://example.com/hook --ssl-verify=false`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		webhookURL, _ := cmd.Flags().GetString("url")
		provider, _ := cmd.Flags().GetString("provider")
		owner, _ := cmd.Flags().GetString("owner")
		sslVerify, _ := cmd.Flags().GetBool("ssl-verify")
		enabled, _ := cmd.Flags().GetBool("enabled")

		if name == "" {
			name = utils.PromptForRequiredInput("Webhook name: ")
		}
		if webhookURL == "" {
			webhookURL = utils.PromptForRequiredInput("Webhook URL: ")
		}

		if provider == "" {
			detected := detectProviderFromURL(webhookURL)
			provider = utils.PromptForInputWithDefault(
				fmt.Sprintf("Provider (slack, discord, teams, telegram, custom) [%s]: ", detected),
				detected,
			)
		}

		if !cmd.Flags().Changed("ssl-verify") {
			input := utils.PromptForInputWithDefault("SSL verification (y/n) [y]: ", "y")
			sslVerify = strings.ToLower(input) != "n" && strings.ToLower(input) != "no"
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if owner == "" {
			if alpaconClient.Username != "" {
				owner = alpaconClient.Username
			} else {
				owner = utils.PromptForRequiredInput("Owner (username): ")
			}
		}

		ownerID, err := iam.GetUserIDByName(alpaconClient, owner)
		if err != nil {
			utils.CliErrorWithExit("Failed to find user: %s.", err)
		}

		request := webhook.WebhookCreateRequest{
			Name:      name,
			URL:       webhookURL,
			Provider:  provider,
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
	var name, webhookURL, provider, owner string
	var sslVerify, enabled bool
	webhookCreateCmd.Flags().StringVar(&name, "name", "", "Webhook name")
	webhookCreateCmd.Flags().StringVar(&webhookURL, "url", "", "Webhook URL")
	webhookCreateCmd.Flags().StringVar(&provider, "provider", "", "Webhook provider (slack, discord, teams, telegram, custom)")
	webhookCreateCmd.Flags().StringVar(&owner, "owner", "", "Owner username")
	webhookCreateCmd.Flags().BoolVar(&sslVerify, "ssl-verify", true, "Enable SSL verification")
	webhookCreateCmd.Flags().BoolVar(&enabled, "enabled", true, "Enable the webhook")
}

func detectProviderFromURL(rawURL string) string {
	lower := strings.ToLower(rawURL)

	if strings.Contains(lower, "slack") {
		return "slack"
	}
	if strings.Contains(lower, "discord") {
		return "discord"
	}
	if strings.Contains(lower, "teams") || strings.Contains(lower, "office") {
		return "teams"
	}
	if strings.Contains(lower, "telegram") {
		return "telegram"
	}
	return "custom"
}
