package approval

import (
	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var approvalCancelCmd = &cobra.Command{
	Use:   "cancel REQUEST_ID",
	Short: "Cancel a pending approval request you submitted",
	Long: `Cancel a pending approval request. Non-superusers may only cancel
requests they personally submitted. Superusers may cancel any
pending request.

Cancelling a work_session request also cancels the linked work
session.`,
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon approval cancel apr-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := approvalapi.CancelRequest(ac, args[0]); err != nil {
			utils.CliErrorWithExit("Failed to cancel request: %s.", err)
		}

		utils.CliSuccess("Approval request %s cancelled.", args[0])
	},
}
