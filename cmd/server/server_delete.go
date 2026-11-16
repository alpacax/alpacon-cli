package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverDeleteCmd = &cobra.Command{
	Use:     "delete SERVER",
	Aliases: []string{"rm"},
	Short:   "Delete a specified server",
	Long: `
	This command is used to permanently delete a specified server from the Alpacon.
	It is crucial to ensure that you have the appropriate permissions to delete a server before attempting this operation.
	The command requires the exact server name as an argument.
	`,
	Example: `
	alpacon server delete my-server
	alpacon server rm my-server
	alpacon server delete my-server -y
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		serverName := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			if proceed, err := utils.Confirm("Delete server '%s'?", serverName); err != nil || !proceed {
				return err
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = server.DeleteServer(alpaconClient, serverName)
		if err != nil {
			return utils.NewExitError("Failed to delete the server: %s.", err)
		}

		utils.CliSuccess("Server deleted: %s", serverName)
		return nil
	}),
}

func init() {
	serverDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
