package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webshDescribeCmd = &cobra.Command{
	Use:     "describe SESSION_ID",
	Aliases: []string{"desc"},
	Short:   "Display detailed information about a websh session",
	Example: `  alpacon websh describe abc123
  alpacon websh desc abc123`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sessionID := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		detail, err := websh.GetSessionDetail(alpaconClient, sessionID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve websh session details: %s.", err)
		}

		utils.PrintJson(detail)
	},
}
