package exec

import (
	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List recent remote command executions",
	Long: `List recent remote command executions, most recent last.

Use --tail to limit the number of entries, --server to scope to one server,
and --user to filter by the requesting user.`,
	Example: `  alpacon exec ls
  alpacon exec ls --tail 10
  alpacon exec ls --tail 10 --server my-server --user admin`,
	Run: func(cmd *cobra.Command, _ []string) {
		pageSize, _ := cmd.Flags().GetInt("tail")
		serverName, _ := cmd.Flags().GetString("server")
		userName, _ := cmd.Flags().GetString("user")

		RunList(pageSize, serverName, userName)
	},
}

func init() {
	listCmd.Flags().IntP("tail", "t", 25, "Number of command entries to show from the end")
	listCmd.Flags().StringP("server", "s", "", "Filter by server name")
	listCmd.Flags().StringP("user", "u", "", "Filter by requesting user")

	ExecCmd.AddCommand(listCmd)
}

// RunList is exported for the deprecated 'alpacon event', which delegates here so the
// two cannot drift. Their flag sets are still declared separately and must stay in sync.
func RunList(pageSize int, serverName, userName string) {
	utils.RequirePositiveInt("tail", pageSize)

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		return
	}

	eventList, err := event.GetEventList(alpaconClient, pageSize, serverName, userName)
	if err != nil {
		utils.CliErrorWithExit("Failed to retrieve the commands: %s.", err)
		return
	}

	utils.PrintTable(eventList)
}
