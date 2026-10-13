package agent

import (
	"github.com/alpacax/alpacon-cli/api/server"
	servercmd "github.com/alpacax/alpacon-cli/cmd/server"
	"github.com/spf13/cobra"
)

var installPAMCmd = &cobra.Command{
	Use:   "install-pam SERVER",
	Short: "Install alpamon-pam on the server",
	Long: `
	This command installs the alpamon-pam package on a specified server through the agent (Alpamon).
	The agent installs it as root with the host's package manager; a server that already has the current version is left as is.
	By default it asks for confirmation; pass -y to skip the prompt.
	It is not disruptive, so it does not check for active user work and has no --force.
	The server refuses it on a host with no alpamon-pam package (macOS, Windows, or an unknown package manager),
	while the workspace's "Use sudo with MFA" setting is off or the deployment cannot complete the MFA step-up, and while an install is already pending on the server.
	`,
	Example: `
	alpacon agent install-pam myserver
	alpacon agent install-pam myserver -y
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		servercmd.RunServerAction(
			args[0],
			server.ActionInstallPAM,
			"Install alpamon-pam on server '%s'?",
			"alpamon-pam installation requested. Run 'alpacon exec ls' to monitor progress.",
			"Failed to install alpamon-pam",
			yes,
		)
	},
}

func init() {
	servercmd.AddConfirmFlag(installPAMCmd)
}
