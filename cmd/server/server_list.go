package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "all"},
	Short:   "Display a list of all servers",
	Long: `
	Display a detailed list of all servers registered in the Alpacon.
	This command provides information such as server ID, name, status, and other relevant details.
	`,
	Example: `
	alpacon server ls
	alpacon server list
	alpacon server all
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		serverList, err := server.GetServerList(alpaconClient)
		if err != nil {
			utils.CliError("Failed to retrieve the servers: %s.", err)
		}

		utils.PrintTable(serverList)
	},
}
