package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webshListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "Display a list of active websh sessions",
	Example: `  alpacon websh ls
  alpacon websh ls --tail 50`,
	Run: func(cmd *cobra.Command, args []string) {
		tail, _ := cmd.Flags().GetInt("tail")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		sessionList, err := websh.GetSessionList(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve websh sessions: %s.", err)
		}

		if tail > 0 && tail < len(sessionList) {
			sessionList = sessionList[len(sessionList)-tail:]
		}

		utils.PrintTable(sessionList)
	},
}

func init() {
	webshListCmd.Flags().Int("tail", 25, "Number of sessions to show")
}
