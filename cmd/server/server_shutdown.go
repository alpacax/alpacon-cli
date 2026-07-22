package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/spf13/cobra"
)

var serverShutdownCmd = &cobra.Command{
	Use:   "shutdown SERVER",
	Short: "Shut down a server's operating system",
	Long: `
	This command shuts down the operating system of a specified server through its agent.
	By default it asks for confirmation; pass -y to skip the prompt.
	If the server has active user work (an open Websh/WebFTP session or an in-flight command), the shutdown is refused unless you pass --force.
	`,
	Example: `
	alpacon server shutdown my-server
	alpacon server shutdown my-server -y
	alpacon server shutdown my-server --force
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		runDisruptiveServerAction(
			args[0],
			server.ActionShutdownSystem,
			"Shut down server '%s'?",
			"System shutdown requested. Run 'alpacon events' to monitor progress.",
			"Failed to shut down the server",
			yes, force,
		)
	},
}

func init() {
	addDisruptiveActionFlags(serverShutdownCmd)
}
