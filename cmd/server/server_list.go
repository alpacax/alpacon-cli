package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List servers",
	Long: `
	Display a detailed list of all servers registered in the Alpacon.
	This command provides information such as server ID, name, status, and other relevant details.
	`,
	Example: `
	alpacon server ls
	alpacon server list
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		serverList, err := server.GetServerList(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the servers: %s.", err)
		}

		utils.PrintTable(serverList)
		return nil
	}),
}
