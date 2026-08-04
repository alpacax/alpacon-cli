package event

import (
	"github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const (
	opWait  = "wait"
	opWatch = "watch"
)

var EventCmd = &cobra.Command{
	Use:     "event",
	Aliases: []string{"events"},
	Short:   "List recent remote command executions (deprecated)",
	Long: `List recent remote command executions, most recent last.

This command has moved to 'alpacon exec ls' and will be removed in a future
release. It still works today and takes the same flags.

The 'event' name is being repurposed for the Alpacon event channel—see
'alpacon event watch'.`,
	Example: `  alpacon event
  alpacon event --tail 10 --server my-server --user admin`,
	// Deprecated is deliberately unused: it makes IsAvailableCommand() false, dropping
	// 'event' from root help and with it the event-channel subcommands landing here.
	Run: runEvent,
}

func init() {
	exec.AddListFlags(EventCmd)
}

func runEvent(cmd *cobra.Command, _ []string) {
	utils.CliWarning("'alpacon event' has moved to 'alpacon exec ls' and will be removed in a future release. 'alpacon event watch' streams the event channel.")

	exec.RunListFromFlags(cmd)
}
