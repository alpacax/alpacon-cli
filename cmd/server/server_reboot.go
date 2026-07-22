package server

import (
	"github.com/spf13/cobra"
)

var rebootServerCmd = &cobra.Command{
	Use:     "reboot SERVER",
	Short:   "Reboot a server's operating system",
	Example: `alpacon server reboot myserver`,
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
	rebootServerCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	rebootServerCmd.Flags().Bool("force", false, "Override the busy guard even when the server has active user work")
}
