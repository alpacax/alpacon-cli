package agent

import (
	"github.com/alpacax/alpacon-cli/api/server"
	servercmd "github.com/alpacax/alpacon-cli/cmd/server"
	"github.com/spf13/cobra"
)

var upgradeAgentCmd = &cobra.Command{
	Use:   "upgrade SERVER",
	Short: "Upgrade server's agent(alpamon)",
	Long: `
	This command upgrades the agent (Alpamon) on a specified server to the latest version.
	By default it asks for confirmation; pass -y to skip the prompt.
	If the server has active user work (an open Websh/WebFTP session or an in-flight command), the upgrade is refused unless you pass --force.
	`,
	Example: `
	alpacon agent upgrade myserver
	alpacon agent upgrade myserver -y
	alpacon agent upgrade myserver --force
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		servercmd.RunDisruptiveServerAction(
			args[0],
			server.ActionUpgradeAgent,
			"Upgrade the agent on server '%s'?",
			"Agent upgrade requested. Run 'alpacon events' to monitor progress.",
			"Failed to upgrade the agent",
			yes, force,
		)
	},
}

func init() {
	servercmd.AddDisruptiveActionFlags(upgradeAgentCmd)
}
