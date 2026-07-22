package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverRefreshCmd = &cobra.Command{
	Use:     "refresh SERVER",
	Short:   "Refresh a server's system information",
	Example: `alpacon server refresh my-server`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serverName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = server.RequestServerAction(alpaconClient, serverName, "update_information", false)
		if err != nil {
			utils.CliErrorWithExit("Failed to refresh the server information: %s.", err)
		}

		utils.CliSuccess("System information refresh requested. Run 'alpacon events' to monitor progress.")
	},
}
