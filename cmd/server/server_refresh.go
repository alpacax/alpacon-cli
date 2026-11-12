package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverRefreshCmd = &cobra.Command{
	Use:   "refresh SERVER",
	Short: "Refresh a server's system information",
	Long: `
	This command requests a refresh of a specified server's system information through its agent.
	It is a non-disruptive action and does not require confirmation.
	`,
	Example: `alpacon server refresh my-server`,
	Args:    cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := refreshServer(alpaconClient, args[0]); err != nil {
			return utils.NewExitError("Failed to refresh the server information: %s.", err)
		}

		utils.CliSuccess("System information refresh requested. Run 'alpacon events' to monitor progress.")
		return nil
	}),
}

func refreshServer(ac *client.AlpaconClient, serverName string) error {
	return requestServerActionWithMFA(ac, serverName, server.ActionUpdateInformation, false)
}
