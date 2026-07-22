package server

import (
	"github.com/spf13/cobra"
)

var serverShutdownCmd = &cobra.Command{
	Use:     "shutdown SERVER",
	Short:   "Shut down a server's operating system",
	Example: `alpacon server shutdown my-server`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		runDisruptiveServerAction(
			args[0],
			"shutdown_system",
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
