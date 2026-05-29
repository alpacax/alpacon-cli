package approval

import (
	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	approveScopes  []string
	approveServers []string
)

var approvalApproveCmd = &cobra.Command{
	Use:   "approve REQUEST_ID",
	Short: "Approve a pending approval request",
	Long: `Approve a pending approval request. Superuser only.

For work_session requests, use --scope and --server to narrow
the granted access at approval time. Omitting these flags
approves the request exactly as submitted.`,
	Args: cobra.ExactArgs(1),
	Example: `  alpacon approval approve apr-abc123
  alpacon approval approve apr-abc123 --scope command --scope websh
  alpacon approval approve apr-abc123 --scope command,websh --server web-01`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		req := approvalapi.ApproveOptions{
			AdjustedScopes: utils.CompactStrings(approveScopes),
		}

		serverIDs, err := server.ResolveServerNames(ac, approveServers)
		if err != nil {
			utils.CliErrorWithExit("Failed to resolve server names: %s.", err)
		}
		req.AdjustedServers = serverIDs

		if err := approvalapi.ApproveRequest(ac, args[0], req); err != nil {
			utils.CliErrorWithExit("Failed to approve request: %s.", err)
		}

		utils.CliSuccess("Approval request %s approved.", args[0])
	},
}

func init() {
	approvalApproveCmd.Flags().StringSliceVar(&approveScopes, "scope", nil, "Adjust granted scopes (work_session only; repeatable or comma-separated)")
	approvalApproveCmd.Flags().StringSliceVar(&approveServers, "server", nil, "Adjust target servers by name (work_session only; repeatable or comma-separated)")
}
