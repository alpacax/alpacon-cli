package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceNotificationsCmd = &cobra.Command{
	Use:     "notifications",
	Aliases: []string{"noti"},
	Short:   "Retrieve workspace notification settings",
	Long:    "Display the current workspace notification settings including disconnection alerts and notification channels.",
	Example: `
	alpacon workspace notifications
	alpacon ws noti`,
	RunE: func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		notificationsDetail, err := workspace.GetNotifications(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve notification settings: %s.", err)
		}

		utils.PrintJson(notificationsDetail)
		return nil
	},
}

func init() {
	workspaceNotificationsCmd.AddCommand(workspaceNotificationsUpdateCmd)
}
