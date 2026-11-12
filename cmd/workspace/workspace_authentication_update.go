package workspace

import (
	"github.com/alpacax/alpacon-cli/api/mfa"
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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		if !utils.IsInteractiveShell() {
			return utils.NewExitError("This command requires an interactive terminal.")
		}

		cfg, err := config.LoadConfig()
		if err != nil {
			return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
		}
		if !cfg.IsSaaS() {
			return utils.NewExitError("This command is only available on Alpacon Cloud workspaces.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		data, err := workspace.EditAuthentication(alpaconClient)
		if err != nil {
			if utils.ReportNoChanges(err) {
				return nil
			}
			return utils.NewExitError("Failed to prepare authentication update: %s.", err)
		}

		var authenticationDetail []byte
		authenticationDetail, err = workspace.PatchAuthentication(alpaconClient, data)
		if err != nil {
			err = utils.HandleCommonErrors(err, "", mfa.WorkspaceErrorCallbacks(alpaconClient, func() error {
				authenticationDetail, err = workspace.PatchAuthentication(alpaconClient, data)
				return err
			}))
			if err != nil {
				return utils.NewExitError("Failed to update authentication settings: %s.", err)
			}
		}

		utils.CliSuccess("Authentication settings updated.")
		utils.PrintJson(authenticationDetail)
		return nil
	}),
}
