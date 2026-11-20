package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newServerUpdateCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:   "update SERVER",
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
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			serverName := args[0]

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			serverDetail, err := server.UpdateServer(alpaconClient, serverName)
			if err != nil {
				if ios.ReportNoChanges(err) {
					return nil
				}
				return utils.NewExitError("Failed to update the server info: %s.", err)
			}

			ios.Success("Server updated: %s", serverName)
			return ios.PrintJson(serverDetail)
		}),
	}
}
