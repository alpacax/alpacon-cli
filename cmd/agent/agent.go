package agent

import (
	"errors"
	"github.com/spf13/cobra"
)

var AgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Commands to manage server's agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon agent upgrade', 'alpacon agent restart', or 'alpacon agent shutdown' to manage the server agent. Run 'alpacon agent --help' for more information")
	},
}

func init() {
	AgentCmd.AddCommand(upgradeAgentCmd)
	AgentCmd.AddCommand(restartAgentCmd)
	AgentCmd.AddCommand(shutdownAgentCmd)
}
