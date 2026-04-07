package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceNotificationsUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update workspace notification settings",
	Long: `Update workspace notification settings by opening the current settings in your editor.
Modify the desired fields, save, and close the editor to apply changes.`,
	Example: `
	alpacon workspace notifications update
	alpacon ws noti update`,
	Run: func(cmd *cobra.Command, args []string) {
		if !utils.IsInteractiveShell() {
			utils.CliErrorWithExit("this command requires an interactive terminal")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		notificationsDetail, err := workspace.UpdateNotifications(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to update notification settings: %s.", err)
		}

		utils.CliSuccess("Notification settings updated.")
		utils.PrintJson(notificationsDetail)
	},
}
