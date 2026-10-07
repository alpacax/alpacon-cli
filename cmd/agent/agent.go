package agent

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var AgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage the Alpamon agent on a server",
	// NoArgs makes an unrecognized subcommand (a removed one included) fail as
	// "unknown command" instead of falling through to the help below.
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	AgentCmd.AddCommand(upgradeAgentCmd)
	AgentCmd.AddCommand(restartAgentCmd)
}
