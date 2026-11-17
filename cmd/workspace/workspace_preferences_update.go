package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newWorkspacePreferencesUpdateCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update workspace preferences",
		Long: `Update workspace preferences by opening the current settings in your editor.
Modify the desired fields, save, and close the editor to apply changes.`,
		Example: `
	alpacon workspace preferences update
	alpacon ws preferences update`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			if !utils.IsInteractiveShell() {
				return utils.NewExitError("This command requires an interactive terminal.")
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			preferencesDetail, err := workspace.UpdatePreferences(alpaconClient)
			if err != nil {
				if ios.ReportNoChanges(err) {
					return nil
				}
				return utils.NewExitError("Failed to update workspace preferences: %s.", err)
			}

			ios.Success("Workspace preferences updated.")
			return ios.PrintJson(preferencesDetail)
		}),
	}
}
