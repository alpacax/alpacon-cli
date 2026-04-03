package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspacePreferencesCmd = &cobra.Command{
	Use:     "preferences",
	Aliases: []string{"prefs"},
	Short: "Retrieve workspace preferences",
	Long:  "Display the current workspace preferences including language, timezone, and other settings.",
	Example: `
	alpacon workspace preferences
	alpacon ws preferences`,
	RunE: func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		preferencesDetail, err := workspace.GetPreferences(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve workspace preferences: %s.", err)
		}

		utils.PrintJson(preferencesDetail)
		return nil
	},
}

func init() {
	workspacePreferencesCmd.AddCommand(workspacePreferencesUpdateCmd)
}
