package approval

import (
	"errors"

	"github.com/spf13/cobra"
)

var (
	statusFilter string
	typeFilter   string
	myRequests   bool
)

var ApprovalCmd = &cobra.Command{
	Use:     "approval",
	Aliases: []string{"req"},
	Short:   "List and manage approval requests",
	Long: `Approval requests are created when actions require administrator
review—work session creation, sudo policy grants, IAM username
conflicts, and service token issuance all generate requests that
a superuser must approve or reject before the action proceeds.

Subcommands for reviewers (superuser only):
  ls        List pending approval requests
  describe  Show details of a request
  approve   Approve a pending request
  reject    Reject a pending request

Subcommands for requesters:
  ls --my   List your own pending requests
  cancel    Cancel a pending request you submitted`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cmd.Help(); err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon approval ls', 'alpacon approval describe', 'alpacon approval approve', 'alpacon approval reject', or 'alpacon approval cancel'. Run 'alpacon approval --help' for more information")
	},
}

func init() {
	ApprovalCmd.AddCommand(approvalListCmd)
	ApprovalCmd.AddCommand(approvalDescribeCmd)
	ApprovalCmd.AddCommand(approvalApproveCmd)
	ApprovalCmd.AddCommand(approvalRejectCmd)
	ApprovalCmd.AddCommand(approvalCancelCmd)
}
