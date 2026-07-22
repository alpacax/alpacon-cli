package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/spf13/cobra"
)

var serverRebootCmd = &cobra.Command{
	Use:   "reboot SERVER",
	Short: "Reboot a server's operating system",
	Long: `
	This command reboots the operating system of a specified server through its agent.
	By default it asks for confirmation; pass -y to skip the prompt.
	If the server has active user work (an open Websh/WebFTP session or an in-flight command), the reboot is refused unless you pass --force.
	`,
	Example: `
	alpacon server reboot my-server
	alpacon server reboot my-server -y
	alpacon server reboot my-server --force
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		runDisruptiveServerAction(
			args[0],
			server.ActionRebootSystem,
			"Reboot server '%s'?",
			"System reboot requested. Run 'alpacon events' to monitor progress.",
			"Failed to reboot the server",
			yes, force,
		)
	},
}

func init() {
	addDisruptiveActionFlags(serverRebootCmd)
}
