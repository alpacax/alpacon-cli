package webhook

import (
	"fmt"
	"strings"

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

Webhook creation requires an Alpacon API that accepts requests without an owner field.`,
	Example: `
  alpacon webhook create
  alpacon webhook create --name=my-webhook --url=https://hooks.slack.com/services/xxx
  alpacon webhook create --name=my-webhook --url=https://example.com/hook --provider=custom
  alpacon webhook create --name=my-webhook --url=https://example.com/hook --ssl-verify=false`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		webhookURL, _ := cmd.Flags().GetString("url")
		provider, _ := cmd.Flags().GetString("provider")
		sslVerify, _ := cmd.Flags().GetBool("ssl-verify")
		enabled, _ := cmd.Flags().GetBool("enabled")

		var err error
		if name == "" {
			if name, err = utils.ReadRequiredInput("Webhook name: "); err != nil {
				return err
			}
		}
		if webhookURL == "" {
			if webhookURL, err = utils.ReadRequiredInput("Webhook URL: "); err != nil {
				return err
			}
		}

		if provider == "" {
			detected := detectProviderFromURL(webhookURL)
			for {
				provider, err = utils.ReadInputWithDefault(
					fmt.Sprintf("Provider (slack, discord, teams, telegram, custom) [%s]: ", detected),
					detected,
				)
				if err != nil {
					return err
				}
				provider = strings.ToLower(strings.TrimSpace(provider))
				if isValidProvider(provider) {
					break
				}
				utils.CliWarning("Invalid provider. Please choose: slack, discord, teams, telegram, custom.")
			}
		} else {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if !isValidProvider(provider) {
				return utils.NewUsageExitError("Invalid provider %q. Supported: slack, discord, teams, telegram, custom.", provider)
			}
		}

		if !cmd.Flags().Changed("ssl-verify") {
			for {
				input, err := utils.ReadInputWithDefault("SSL verification (y/n) [y]: ", "y")
				if err != nil {
					return err
				}
				value := strings.ToLower(strings.TrimSpace(input))
				if value == "y" || value == "yes" {
					sslVerify = true
					break
				}
				if value == "n" || value == "no" {
					sslVerify = false
					break
				}
				utils.CliWarning("Invalid input. Please enter 'y' or 'n'.")
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		request := webhook.WebhookCreateRequest{
			Name:      name,
			URL:       webhookURL,
			Provider:  provider,
			SSLVerify: sslVerify,
			Enabled:   enabled,
		}

		err = webhook.CreateWebhook(alpaconClient, request)
		if err != nil {
			return utils.NewExitError("Failed to create webhook: %s.", err)
		}

		utils.CliSuccess("Webhook created: %s", name)
		return nil
	}),
}

func init() {
	var name, webhookURL, provider string
	var sslVerify, enabled bool
	webhookCreateCmd.Flags().StringVar(&name, "name", "", "Webhook name")
	webhookCreateCmd.Flags().StringVar(&webhookURL, "url", "", "Webhook URL")
	webhookCreateCmd.Flags().StringVar(&provider, "provider", "", "Webhook provider (slack, discord, teams, telegram, custom)")
	webhookCreateCmd.Flags().BoolVar(&sslVerify, "ssl-verify", true, "Enable SSL verification")
	webhookCreateCmd.Flags().BoolVar(&enabled, "enabled", true, "Enable the webhook")
}

func isValidProvider(p string) bool {
	switch p {
	case "slack", "discord", "teams", "telegram", "custom":
		return true
	}
	return false
}

func detectProviderFromURL(rawURL string) string {
	lower := strings.ToLower(rawURL)

	if strings.Contains(lower, "slack") {
		return "slack"
	}
	if strings.Contains(lower, "discord") {
		return "discord"
	}
	if strings.Contains(lower, "teams") || strings.Contains(lower, "office") || strings.Contains(lower, "azure") {
		return "teams"
	}
	if strings.Contains(lower, "telegram") {
		return "telegram"
	}
	return "custom"
}
