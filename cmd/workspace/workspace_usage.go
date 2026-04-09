package workspace

import (
	"encoding/json"

	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Retrieve workspace usage and billing estimate",
	Long:  "Display the current billing period usage and cost estimate for the workspace.",
	Example: `
	alpacon workspace usage
	alpacon ws usage`,
	RunE: func(cmd *cobra.Command, args []string) error {
		isSaaS, err := config.IsSaaS()
		if err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' first.")
		}
		if !isSaaS {
			utils.CliErrorWithExit("This command is only available on Alpacon Cloud workspaces.")
		}

		cfg, err := config.LoadConfig()
		if err != nil {
			utils.CliErrorWithExit("Failed to load configuration: %s.", err)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		paymentBaseURL, err := workspace.GetPaymentAPIBaseURL(cfg.WorkspaceURL)
		if err != nil {
			utils.CliErrorWithExit("Failed to determine payment API URL: %s.", err)
		}

		workspaceID, err := workspace.GetWorkspaceID(alpaconClient, paymentBaseURL, cfg.WorkspaceName)
		if err != nil {
			utils.CliErrorWithExit("Failed to find workspace: %s.", err)
		}

		estimate, err := workspace.GetUsageEstimate(alpaconClient, paymentBaseURL, workspaceID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve usage estimate: %s.", err)
		}

		data, err := json.Marshal(estimate)
		if err != nil {
			utils.CliErrorWithExit("Failed to format usage data: %s.", err)
		}
		utils.PrintJson(data)
		return nil
	},
}
