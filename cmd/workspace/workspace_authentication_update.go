package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceAuthenticationUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update workspace authentication settings",
	Long: `Update workspace authentication settings by opening the current settings in your editor.
Modify the desired fields, save, and close the editor to apply changes.`,
	Example: `
	alpacon workspace authentication update
	alpacon ws auth update`,
	Run: func(cmd *cobra.Command, args []string) {
		if !utils.IsInteractiveShell() {
			utils.CliErrorWithExit("This command requires an interactive terminal.")
		}

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

		data, err := workspace.EditAuthentication(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to prepare authentication update: %s.", err)
		}

		var authenticationDetail []byte
		authenticationDetail, err = workspace.PatchAuthentication(alpaconClient, data)
		if err != nil {
			err = utils.HandleCommonErrors(err, "", errorCallbacks(alpaconClient, func() error {
				authenticationDetail, err = workspace.PatchAuthentication(alpaconClient, data)
				return err
			}))
			if err != nil {
				utils.CliErrorWithExit("Failed to update authentication settings: %s.", err)
			}
		}

		utils.CliSuccess("Authentication settings updated.")
		utils.PrintJson(authenticationDetail)
	},
}
