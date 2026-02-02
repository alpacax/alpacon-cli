package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverDeleteCmd = &cobra.Command{
	Use:     "delete [SERVER NAME]",
	Aliases: []string{"rm"},
	Short:   "Delete a specified server",
	Long: `
	This command is used to permanently delete a specified server from the Alpacon. 
	It is crucial to ensure that you have the appropriate permissions to delete a server before attempting this operation. 
	The command requires the exact server name as an argument.
	`,
	Example: ` 
	alpacon server delete [SERVER NAME]	
	alpacon server rm [SERVER NAME]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serverName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = server.DeleteServer(alpaconClient, serverName)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the server: %s.", err)
		}

		utils.CliInfo("Server successfully deleted: %s.", serverName)
	},
}
