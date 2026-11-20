package approval

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

const approvalGuidance = "Approval and rejection happen in the Alpacon console (web) or Slack."

func NewApprovalCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
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
		Args: utils.RejectUnknownSubcommand(approvalGuidance),
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd, approvalGuidance)
		},
	}
	cmd.AddCommand(
		newApprovalListCmd(newClient, ios),
		newApprovalDescribeCmd(newClient, ios),
		newApprovalApproveCmd(),
		newApprovalRejectCmd(),
		newApprovalCancelCmd(newClient, ios),
	)
	return cmd
}
