package approval

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// approveRejectExcludedMessage explains why the CLI no longer performs approve or
// reject. ADR 0015 makes the CLI an execution/request surface only: approval
// happens out of band in the Alpacon console (web/Slack), and the server now
// refuses (HTTP 403) any approve/reject coming from the CLI credential channel.
// The subcommands remain registered so the message is discoverable and existing
// scripts get an actionable, intentional exit instead of an "unknown command".
const approveRejectExcludedMessage = "Approvals must be done in the Alpacon console (web), not the CLI. " +
	"The CLI is an execution and request surface only; approve and reject happen out of band. " +
	"Use 'alpacon approval ls' to track a request's status."

var approvalApproveCmd = &cobra.Command{
	Use:   "approve REQUEST_ID",
	Short: "Approve a request (moved to the Alpacon console)",
	Long: `Approving requests has moved to the Alpacon console (web).

The CLI is an execution and request surface only; a human approves or rejects
out of band in the web console or Slack. The server rejects approve/reject from
the CLI credential channel. Use 'alpacon approval ls' to track status.`,
	Args: cobra.ArbitraryArgs,
	Example: `  # Approve in the Alpacon console (web), then track status here:
  alpacon approval ls --status approved`,
	Run: func(cmd *cobra.Command, args []string) {
		// Exit non-zero so a script that expected the approval to happen does not
		// mistake this for success—the CLI must never pretend to approve.
		utils.CliErrorWithExit("%s", approveRejectExcludedMessage)
	},
}
