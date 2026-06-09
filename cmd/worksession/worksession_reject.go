package worksession

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionRejectCmd = &cobra.Command{
	Use:   "reject SESSION_ID",
	Short: "Reject a session (moved to the Alpacon console)",
	Long: `Rejecting work sessions has moved to the Alpacon console (web).

The CLI is an execution and request surface only; a human approves or rejects
out of band in the web console or Slack. The server rejects approve/reject from
the CLI credential channel. Use 'alpacon work-session ls' to track status.`,
	Args: cobra.ArbitraryArgs,
	Example: `  # Reject in the Alpacon console (web), then track status here:
  alpacon work-session ls --status rejected`,
	Run: func(cmd *cobra.Command, args []string) {
		// Exit non-zero so a script that expected the reject to happen does not
		// mistake this for success—the CLI must never pretend to reject.
		utils.CliErrorWithExit("%s", approveRejectExcludedMessage)
	},
}
