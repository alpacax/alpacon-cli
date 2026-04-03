package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspacePreferencesUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update workspace preferences",
	Long: `Update workspace preferences by opening the current settings in your editor.
Modify the desired fields, save, and close the editor to apply changes.`,
	Example: `
	alpacon workspace preferences update
	alpacon ws preferences update`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		preferencesDetail, err := workspace.UpdatePreferences(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to update workspace preferences: %s.", err)
		}

		utils.CliSuccess("Workspace preferences updated.")
		utils.PrintJson(preferencesDetail)
	},
}
