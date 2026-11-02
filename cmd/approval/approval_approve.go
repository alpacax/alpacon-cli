package approval

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// approveRejectExcludedMessage explains why the CLI no longer approves or rejects. The server
// refuses (HTTP 403) approve/reject from the CLI credential channel; the subcommands stay
// registered so existing scripts get this message and a non-zero exit instead of "unknown command".
const approveRejectExcludedMessage = "Approvals must be done in the Alpacon console (web), not the CLI. " +
	"The CLI is an execution and request surface only; approval and rejection happen out of band. " +
	"Use 'alpacon approval ls' to track a request's status."

var approvalApproveCmd = &cobra.Command{
	Use:   "approve",
	Short: "Approve a request (moved to the Alpacon console)",
	Long: `Approving requests has moved to the Alpacon console (web).

The CLI is an execution and request surface only; a human approves or rejects
out of band in the web console or Slack. The server rejects approve/reject from
the CLI credential channel. Use 'alpacon approval ls' to track status.`,
	Args: cobra.ArbitraryArgs,
	Example: `  # Approve in the Alpacon console (web), then track status here:
  alpacon approval ls --status approved`,
	Run: func(cmd *cobra.Command, args []string) {
		// Non-zero exit: a script that expected the approval must not read this as success.
		utils.CliErrorWithExit("%s", approveRejectExcludedMessage)
	},
}
