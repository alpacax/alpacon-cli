package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceAccessControlUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update workspace access control settings",
	Long: `Update workspace access control settings by opening the current settings in your editor.
Modify the desired fields, save, and close the editor to apply changes.`,
	Example: `
	alpacon workspace access-control update
	alpacon ws acl update`,
	Run: func(cmd *cobra.Command, args []string) {
		if !utils.IsInteractiveShell() {
			utils.CliErrorWithExit("This command requires an interactive terminal.")
		}

		// No IsSaaS() gate, unlike the sibling authentication update: the server
		// registers access-control outside AUTH0_ENABLED and the viewset has an
		// onprem path, so this works self-hosted too.
		if _, err := config.LoadConfig(); err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' first.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		data, err := workspace.EditAccessControl(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to prepare access control update: %s.", err)
		}

		var accessControlDetail []byte
		accessControlDetail, err = workspace.PatchAccessControl(alpaconClient, data)
		if err != nil {
			err = utils.HandleCommonErrors(err, "", errorCallbacks(alpaconClient, func() error {
				accessControlDetail, err = workspace.PatchAccessControl(alpaconClient, data)
				return err
			}))
			if err != nil {
				utils.CliErrorWithExit("Failed to update access control settings: %s.", err)
			}
		}

		utils.CliSuccess("Access control settings updated.")
		utils.PrintJson(accessControlDetail)
	},
}
