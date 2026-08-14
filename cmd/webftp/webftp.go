package webftp

import (
	"github.com/alpacax/alpacon-cli/api/webftp"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var WebFTPCmd = &cobra.Command{
	Use:     "webftp-log",
	Aliases: []string{"webftp-logs"},
	Short:   "Retrieve and display WebFTP transfer logs",
	Long: `
	Retrieve and display WebFTP file transfer logs from the Alpacon, with options to filter by
	server, user, and action. Use the '--tail' flag to show the newest N entries.
	`,
	Example: `
	alpacon webftp-log
	alpacon webftp-logs
	alpacon webftp-log --tail 10 --server my-server
	alpacon webftp-log --tail=50 --user=admin --action=upload
	`,
	Run: runWebFTP,
}

func init() {
	var tail int
	var serverName string
	var userName string
	var action string

	WebFTPCmd.Flags().IntVarP(&tail, "tail", "t", 25, "Number of log entries to show, newest first")
	WebFTPCmd.Flags().StringVarP(&serverName, "server", "s", "", "Filter by server name")
	WebFTPCmd.Flags().StringVarP(&userName, "user", "u", "", "Filter by username")
	WebFTPCmd.Flags().StringVarP(&action, "action", "a", "", "Filter by action (e.g., upload, download)")
}

func runWebFTP(cmd *cobra.Command, args []string) {
	tail, _ := cmd.Flags().GetInt("tail")
	utils.RequirePositiveInt("tail", tail)
	serverName, _ := cmd.Flags().GetString("server")
	userName, _ := cmd.Flags().GetString("user")
	action, _ := cmd.Flags().GetString("action")

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	logList, err := webftp.GetWebFTPLogList(alpaconClient, tail, serverName, userName, action)
	if err != nil {
		utils.CliErrorWithExit("Failed to get WebFTP logs: %s.", err)
	}

	utils.PrintTable(logList)
}
