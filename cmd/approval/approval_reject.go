package approval

import (
	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var approvalRejectCmd = &cobra.Command{
	Use:     "reject REQUEST_ID",
	Short:   "Reject a pending approval request",
	Long:    "Reject a pending approval request. Superuser only.",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon approval reject apr-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := approvalapi.RejectRequest(ac, args[0]); err != nil {
			utils.CliErrorWithExit("Failed to reject request: %s.", err)
		}

		utils.CliSuccess("Approval request %s rejected.", args[0])
	},
}
