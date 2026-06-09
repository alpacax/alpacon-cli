package approval

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var approvalRejectCmd = &cobra.Command{
	Use:   "reject REQUEST_ID",
	Short: "Reject a request (moved to the Alpacon console)",
	Long: `Rejecting requests has moved to the Alpacon console (web).

The CLI is an execution and request surface only; a human approves or rejects
out of band in the web console or Slack. The server rejects approve/reject from
the CLI credential channel. Use 'alpacon approval ls' to track status.`,
	Args: cobra.ArbitraryArgs,
	Example: `  # Reject in the Alpacon console (web), then track status here:
  alpacon approval ls --status rejected`,
	Run: func(cmd *cobra.Command, args []string) {
		// Exit non-zero so a script that expected the reject to happen does not
		// mistake this for success—the CLI must never pretend to reject.
		utils.CliErrorWithExit("%s", approveRejectExcludedMessage)
	},
}
