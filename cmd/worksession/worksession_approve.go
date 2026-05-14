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
	Short: "Approve a pending work session (superuser only)",
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

		var filteredScopes []string
		for _, s := range approveScopes {
			if s != "" {
				filteredScopes = append(filteredScopes, s)
			}
		}
		req := wsapi.WorkSessionApproveRequest{
			AdjustedScopes: filteredScopes,
		}

		if len(approveServers) > 0 {
			serverIDs := make([]string, 0, len(approveServers))
			for _, name := range approveServers {
				id, err := server.GetServerIDByName(ac, name)
				if err != nil {
					utils.CliErrorWithExit("Server %q not found: %s.", name, err)
				}
				serverIDs = append(serverIDs, id)
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
