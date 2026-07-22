package server

import (
	"github.com/spf13/cobra"
)

var serverUpgradeCmd = &cobra.Command{
	Use:     "upgrade SERVER",
	Short:   "Upgrade a server's operating system packages",
	Example: `alpacon server upgrade my-server`,
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
	addDisruptiveActionFlags(serverUpgradeCmd)
}
