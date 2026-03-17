package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webshCloseCmd = &cobra.Command{
	Use:   "close SESSION_ID",
	Short: "Close a websh session",
	Example: `  alpacon websh close abc123`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sessionID := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = websh.CloseSession(alpaconClient, sessionID)
		if err != nil {
			utils.CliErrorWithExit("Failed to close websh session: %s.", err)
		}

		utils.CliSuccess("Session '%s' has been closed.", sessionID)
	},
}
