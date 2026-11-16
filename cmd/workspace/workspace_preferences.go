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
	Short:   "Retrieve workspace preferences",
	Long:    "Display the current workspace preferences including language, timezone, and other settings.",
	Example: `
	alpacon workspace preferences
	alpacon ws preferences`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		preferencesDetail, err := workspace.GetPreferences(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve workspace preferences: %s.", err)
		}

		return utils.PrintJson(preferencesDetail)
	}),
}

func init() {
	workspacePreferencesCmd.AddCommand(workspacePreferencesUpdateCmd)
}
