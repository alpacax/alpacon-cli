package workspace

import (
	"encoding/json"

	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// noLimitText replaces a null service limit for display: a null limit means
// unlimited or fair use (never a plan-limit refusal on that axis, paywall wave
// wire contract §1.2), and a raw JSON `null` reads as a bug in the terminal
// rather than as the fair-use answer it is.
const noLimitText = "fair use / no cap"

// renderedServiceUsage mirrors workspace.ServiceUsage for display, with Limit
// widened to `any` so a null limit becomes noLimitText instead of `null`.
type renderedServiceUsage struct {
	Name         string  `json:"name"`
	Unit         string  `json:"unit"`
	Limit        any     `json:"limit"`
	CurrentUsage float64 `json:"current_usage"`
	CurrentCost  string  `json:"current_cost"`
	OverageCost  *string `json:"overage_cost"`
}

// renderedUsageEstimate mirrors workspace.UsageEstimate for display, with
// Services rendered through renderedServiceUsage.
type renderedUsageEstimate struct {
	BillingPeriod workspace.BillingPeriod         `json:"billing_period"`
	Currency      string                          `json:"currency"`
	Subscription  workspace.Subscription          `json:"subscription"`
	Services      map[string]renderedServiceUsage `json:"services"`
	Metadata      *workspace.UsageMetadata        `json:"metadata"`
}

// renderUsageEstimate copies estimate for display, replacing every service's
// null Limit with noLimitText.
func renderUsageEstimate(estimate *workspace.UsageEstimate) renderedUsageEstimate {
	services := make(map[string]renderedServiceUsage, len(estimate.Services))
	for key, svc := range estimate.Services {
		var limit any = noLimitText
		if svc.Limit != nil {
			limit = *svc.Limit
		}
		services[key] = renderedServiceUsage{
			Name:         svc.Name,
			Unit:         svc.Unit,
			Limit:        limit,
			CurrentUsage: svc.CurrentUsage,
			CurrentCost:  svc.CurrentCost,
			OverageCost:  svc.OverageCost,
		}
	}
	return renderedUsageEstimate{
		BillingPeriod: estimate.BillingPeriod,
		Currency:      estimate.Currency,
		Subscription:  estimate.Subscription,
		Services:      services,
		Metadata:      estimate.Metadata,
	}
}

var workspaceUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Retrieve workspace usage and billing estimate",
	Long:  "Display the current billing period usage and cost estimate for the workspace.",
	Example: `
	alpacon workspace usage
	alpacon ws usage`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' first.")
		}
		if !cfg.IsSaaS() {
			utils.CliErrorWithExit("This command is only available on Alpacon Cloud workspaces.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		paymentBaseURL, err := workspace.GetPaymentAPIBaseURL(alpaconClient.BaseURL)
		if err != nil {
			utils.CliErrorWithExit("Failed to determine payment API URL: %s.", err)
		}

		workspaceID, err := workspace.GetWorkspaceID(alpaconClient, paymentBaseURL, alpaconClient.WorkspaceName)
		if err != nil {
			utils.CliErrorWithExit("Failed to find workspace: %s.", err)
		}

		estimate, err := workspace.GetUsageEstimate(alpaconClient, paymentBaseURL, workspaceID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve usage estimate: %s.", err)
		}

		data, err := json.Marshal(renderUsageEstimate(estimate))
		if err != nil {
			utils.CliErrorWithExit("Failed to format usage data: %s.", err)
		}
		utils.PrintJson(data)
		return nil
	},
}
