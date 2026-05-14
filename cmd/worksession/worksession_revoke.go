package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionRevokeCmd = &cobra.Command{
	Use:     "revoke SESSION_ID",
	Short:   "Force-terminate an active or approved work session (superuser only)",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session revoke ses-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := wsapi.RevokeWorkSession(ac, args[0]); err != nil {
			utils.CliErrorWithExit("Failed to revoke work session: %s.", err)
		}

		utils.CliSuccess("Work session %s revoked.", args[0])
	},
}
