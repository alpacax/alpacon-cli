package worksession

import (
	"github.com/alpacax/alpacon-cli/api/server"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	approveScopes  []string
	approveServers []string
)

var workSessionApproveCmd = &cobra.Command{
	Use:   "approve SESSION_ID",
	Short: "Approve a pending work session",
	Long: `Approve a pending work session. Superuser only.

Without flags, approves the session as originally requested.
Use --scope and --server to narrow down the granted access at approval time.`,
	Args: cobra.ExactArgs(1),
	Example: `  alpacon work-session approve ses-abc123
  alpacon work-session approve ses-abc123 --scope command --scope websh
  alpacon work-session approve ses-abc123 --scope command,websh --server web-01`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		req := wsapi.WorkSessionApproveRequest{
			AdjustedScopes: utils.CompactStrings(approveScopes),
		}

		if len(approveServers) > 0 {
			serverIDs, err := server.ResolveServerNames(ac, approveServers)
			if err != nil {
				utils.CliErrorWithExit("%s.", err)
			}
			req.AdjustedServers = serverIDs
		}

		if err := wsapi.ApproveWorkSession(ac, args[0], req); err != nil {
			utils.CliErrorWithExit("Failed to approve work session: %s.", err)
		}

		utils.CliSuccess("Work session %s approved.", args[0])
	},
}

func init() {
	workSessionApproveCmd.Flags().StringSliceVar(&approveScopes, "scope", nil, "Adjust granted scopes (repeatable; comma-separated values also accepted)")
	workSessionApproveCmd.Flags().StringSliceVar(&approveServers, "server", nil, "Adjust target servers by name (repeatable; comma-separated values also accepted)")
}
