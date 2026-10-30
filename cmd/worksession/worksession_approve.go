package worksession

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// approveRejectExcludedMessage explains why the CLI does not approve or reject work sessions:
// approval happens in the Alpacon console, and the server refuses it (403) from the CLI. The
// subcommands stay so existing scripts get an actionable exit instead of "unknown command".
const approveRejectExcludedMessage = "Approvals must be done in the Alpacon console (web), not the CLI. " +
	"The CLI is an execution and request surface only; approval and rejection happen out of band. " +
	"Use 'alpacon work-session ls' to track a session's status."

var workSessionApproveCmd = &cobra.Command{
	Use:   "approve",
	Short: "Approve a session (moved to the Alpacon console)",
	Long: `Approving work sessions has moved to the Alpacon console (web).

The CLI is an execution and request surface only; a human approves or rejects
out of band in the web console or Slack. The server rejects approve/reject from
the CLI credential channel. Use 'alpacon work-session ls' to track status.`,
	Args: cobra.ArbitraryArgs,
	Example: `  # Approve in the Alpacon console (web), then track status here:
  alpacon work-session ls --status active`,
	Run: func(cmd *cobra.Command, args []string) {
		// Exit non-zero so a script that expected the approval to happen does not
		// mistake this for success—the CLI must never pretend to approve.
		utils.CliErrorWithExit("%s", approveRejectExcludedMessage)
	},
}
