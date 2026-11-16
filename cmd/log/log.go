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
	to show the newest N log entries. Suitable for debugging and monitoring 
	server activities.`,
	Example: `
	alpacon log my-server
	alpacon logs my-server
	alpacon log my-server --tail=10
	alpacon logs my-server --tail=10
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		serverName := args[0]
		tail, _ := cmd.Flags().GetInt("tail")
		if err := utils.CheckPositiveInt("tail", tail); err != nil {
			return err
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		logList, err := log.GetSystemLogList(alpaconClient, serverName, tail)
		return utils.PrintList(logList, err, "Failed to get logs")
	}),
}

func init() {
	var tail int

	LogCmd.Flags().IntVarP(&tail, "tail", "t", 25, "Number of log entries to show, newest first")
}
