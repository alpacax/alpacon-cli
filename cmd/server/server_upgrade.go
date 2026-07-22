package server

import (
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/spf13/cobra"
)

var serverUpgradeCmd = &cobra.Command{
	Use:   "upgrade SERVER",
	Short: "Upgrade a server's operating system packages",
	Long: `
	This command upgrades the operating system packages of a specified server through its agent.
	By default it asks for confirmation; pass -y to skip the prompt.
	If the server has active user work (an open Websh/WebFTP session or an in-flight command), the upgrade is refused unless you pass --force.
	`,
	Example: `
	alpacon server upgrade my-server
	alpacon server upgrade my-server -y
	alpacon server upgrade my-server --force
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		runDisruptiveServerAction(
			args[0],
			server.ActionUpgradeSystem,
			"Upgrade server '%s'?",
			"System upgrade requested. Run 'alpacon events' to monitor progress.",
			"Failed to upgrade the server",
			yes, force,
		)
	},
}

func init() {
	addDisruptiveActionFlags(serverUpgradeCmd)
}
