package agent

import (
	"github.com/alpacax/alpacon-cli/api/server"
	servercmd "github.com/alpacax/alpacon-cli/cmd/server"
	"github.com/spf13/cobra"
)

var restartAgentCmd = &cobra.Command{
	Use:   "restart SERVER",
	Short: "Restart server's agent(alpamon)",
	Long: `
	This command restarts the agent (Alpamon) on a specified server.
	By default it asks for confirmation; pass -y to skip the prompt.
	If the server has active user work (an open Websh/WebFTP session or an in-flight command), the restart is refused unless you pass --force.
	`,
	Example: `
	alpacon agent restart myserver
	alpacon agent restart myserver -y
	alpacon agent restart myserver --force
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		servercmd.RunDisruptiveServerAction(
			args[0],
			server.ActionRestartAgent,
			"Restart the agent on server '%s'?",
			"Agent restart requested. Run 'alpacon events' to monitor progress.",
			"Failed to restart the agent",
			yes, force,
		)
	},
}

func init() {
	servercmd.AddDisruptiveActionFlags(restartAgentCmd)
}
