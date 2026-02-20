package log

import (
	"github.com/alpacax/alpacon-cli/api/log"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var LogCmd = &cobra.Command{
	Use:     "log SERVER",
	Aliases: []string{"logs"},
	Short:   "Retrieve and display server logs",
	Long: `Retrieve and display logs for a specified server. This command allows you 
	to view logs of different levels and types associated with a server. Use the '--tail' flag 
	to limit the output to the last N log entries. Suitable for debugging and monitoring 
	server activities.`,
	Example: `
	alpacon log [SERVER NAME]
	alpacon logs [SERVER_NAME]
	alpacon log [SERVER NAME] --tail=10
	alpacon logs [SERVER NAME] --tail=10
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serverName := args[0]
		pageSize, _ := cmd.Flags().GetInt("tail")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		logList, err := log.GetSystemLogList(alpaconClient, serverName, pageSize)
		if err != nil {
			utils.CliErrorWithExit("Failed to get logs: %s.", err)
		}

		utils.PrintTable(logList)
	},
}

func init() {
	var pageSize int

	LogCmd.Flags().IntVarP(&pageSize, "tail", "t", 25, "Number of log entries to show from the end")
}
