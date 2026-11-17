package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newWorkspacePreferencesCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "preferences",
		Aliases: []string{"prefs"},
		Short:   "Retrieve workspace preferences",
		Long:    "Display the current workspace preferences including language, timezone, and other settings.",
		Example: `
	alpacon workspace preferences
	alpacon ws preferences`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			preferencesDetail, err := workspace.GetPreferences(alpaconClient)
			if err != nil {
				return utils.NewExitError("Failed to retrieve workspace preferences: %s.", err)
			}

			return ios.PrintJson(preferencesDetail)
		}),
	}
	cmd.AddCommand(newWorkspacePreferencesUpdateCmd(newClient, ios))
	return cmd
}
