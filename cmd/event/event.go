package event

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const (
	opWait  = "wait"
	opWatch = "watch"
)

// movedGuidance points a user of the removed bare 'alpacon event' list to its replacement.
const movedGuidance = "The command list moved to 'alpacon exec ls'."

var EventCmd = &cobra.Command{
	Use:     "event",
	Aliases: []string{"events"},
	Short:   "Watch and wait on the Alpacon event channel",
	Long: `Watch and wait on the Alpacon event channel.

To list recent command executions, use 'alpacon exec ls'.`,
	Args: utils.RejectUnknownSubcommand(movedGuidance),
	RunE: func(cmd *cobra.Command, _ []string) error {
		return utils.RequireSubcommand(cmd, movedGuidance)
	},
}
