package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverUpdateCmd = &cobra.Command{
	Use:   "update [SERVER NAME]",
	Short: "Update the server information",
	Long: `
	Update the server information in the Alpacon.
	This command opens your editor with the current server data, allowing you to modify fields such as
	name, groups, and other configuration. Not all fields may be editable depending on your permissions.
	After saving, the updated server information is displayed for verification.
	`,
	Example: `
	alpacon server update my-server
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serverName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		serverDetail, err := server.UpdateServer(alpaconClient, serverName)
		if err != nil {
			utils.CliErrorWithExit("Failed to update the server info: %s.", err)
		}

		utils.CliSuccess("Server updated: %s", serverName)
		utils.PrintJson(serverDetail)
	},
}
