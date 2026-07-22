package server

import (
	"github.com/spf13/cobra"
)

var upgradeServerCmd = &cobra.Command{
	Use:     "upgrade SERVER",
	Short:   "Upgrade a server's operating system packages",
	Example: `alpacon server upgrade myserver`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		runDisruptiveServerAction(
			args[0],
			"upgrade_system",
			"Upgrade system on server '%s'?",
			"System upgrade requested. Run 'alpacon events' to monitor progress.",
			"Failed to upgrade the server",
			yes, force,
		)
	},
}

func init() {
	upgradeServerCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	upgradeServerCmd.Flags().Bool("force", false, "Override the busy guard even when the server has active user work")
}
