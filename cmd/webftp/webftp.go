package webftp

import (
	"github.com/alpacax/alpacon-cli/api/webftp"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

var WebFTPCmd = newWebFTPCmd(client.NewAlpaconAPIClient)

func newWebFTPCmd(newClient clientFactory) *cobra.Command {
	cmd := &cobra.Command{
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
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			return runWebFTP(cmd, args, newClient)
		}),
	}
	var action string
	var userName string
	var serverName string
	var tail int
	cmd.Flags().IntVarP(&tail, "tail", "t", 25, "Number of log entries to show, newest first")
	cmd.Flags().StringVarP(&serverName, "server", "s", "", "Filter by server name")
	cmd.Flags().StringVarP(&userName, "user", "u", "", "Filter by username")
	cmd.Flags().StringVarP(&action, "action", "a", "", "Filter by action (e.g., upload, download)")
	return cmd
}

func runWebFTP(cmd *cobra.Command, args []string, newClient clientFactory) error {
	tail, _ := cmd.Flags().GetInt("tail")
	if err := utils.CheckPositiveInt("tail", tail); err != nil {
		return err
	}
	serverName, _ := cmd.Flags().GetString("server")
	userName, _ := cmd.Flags().GetString("user")
	action, _ := cmd.Flags().GetString("action")

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	logList, err := webftp.GetWebFTPLogList(alpaconClient, tail, serverName, userName, action)
	return utils.PrintList(logList, err, "Failed to get WebFTP logs")
}
