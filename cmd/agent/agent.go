package agent

import (
	"errors"

	"github.com/spf13/cobra"
)

var AgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage the Alpamon agent on a server",
	// NoArgs makes an unrecognized subcommand (a removed one included) fail as
	// "unknown command" instead of falling through to the help below.
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon agent upgrade' or 'alpacon agent restart' to manage the server agent. Run 'alpacon agent --help' for more information")
	},
}

func init() {
	AgentCmd.AddCommand(upgradeAgentCmd)
	AgentCmd.AddCommand(restartAgentCmd)
}
