package approval

import (
	"errors"

	"github.com/spf13/cobra"
)

var ApprovalCmd = &cobra.Command{
	Use:     "approval",
	Aliases: []string{"req"},
	Short:   "List and manage approval requests",
	Long: `Approval requests are created when actions require administrator
review—work session creation, sudo policy grants, IAM username
conflicts, and service token issuance all generate requests that
a human must approve or reject before the action proceeds.

Approving and rejecting happen out of band in the Alpacon console
(web) or Slack, not the CLI. The CLI is an execution and request
surface only; use it to create requests and track their status.

Subcommands for tracking:
  ls        List approval requests (--my for your own)
  describe  Show details of a request
  cancel    Cancel a pending request you submitted`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cmd.Help(); err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon approval ls', 'alpacon approval describe', or 'alpacon approval cancel'. Approve and reject happen in the Alpacon console (web). Run 'alpacon approval --help' for more information")
	},
}

func init() {
	ApprovalCmd.AddCommand(approvalListCmd)
	ApprovalCmd.AddCommand(approvalDescribeCmd)
	ApprovalCmd.AddCommand(approvalApproveCmd)
	ApprovalCmd.AddCommand(approvalRejectCmd)
	ApprovalCmd.AddCommand(approvalCancelCmd)
}
