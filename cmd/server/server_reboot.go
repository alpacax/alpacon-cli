package server

import (
	"github.com/spf13/cobra"
)

var serverRebootCmd = &cobra.Command{
	Use:     "reboot SERVER",
	Short:   "Reboot a server's operating system",
	Example: `alpacon server reboot my-server`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		runDisruptiveServerAction(
			args[0],
			"reboot_system",
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
