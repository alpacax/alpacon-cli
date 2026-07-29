package event

import (
	"github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var EventCmd = &cobra.Command{
	Use:     "event",
	Aliases: []string{"events"},
	Short:   "List recent remote command executions (deprecated: use 'exec ls')",
	Long: `List recent remote command executions, most recent last.

This command has moved to 'alpacon exec ls' and will be removed in a future
release. It still works today and takes the same flags.

The 'event' name is being repurposed for the Alpacon event channel.`,
	Example: `  alpacon event
  alpacon event --tail 10 --server my-server --user admin`,
	// Deprecated is deliberately unused: it makes IsAvailableCommand() false, dropping
	// 'event' from root help and with it the event-channel subcommands landing here.
	Run: runEvent,
}

func init() {
	EventCmd.Flags().IntP("tail", "t", 25, "Number of command entries to show from the end")
	EventCmd.Flags().StringP("server", "s", "", "Filter by server name")
	EventCmd.Flags().StringP("user", "u", "", "Filter by requesting user")
}

func runEvent(cmd *cobra.Command, _ []string) {
	utils.CliWarning("'alpacon event' has moved to 'alpacon exec ls' and will be removed in a future release.")

	pageSize, _ := cmd.Flags().GetInt("tail")
	serverName, _ := cmd.Flags().GetString("server")
	userName, _ := cmd.Flags().GetString("user")

	exec.RunList(pageSize, serverName, userName)
}
