package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionRevokeCmd = &cobra.Command{
	Use:     "revoke SESSION_ID",
	Short:   "Force-terminate an active or approved work session",
	Long:    "Force-terminate an active or approved work session. Superuser only.",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session revoke ses-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opRevoke, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		cleared, err := RunRevoke(ac, args[0])
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opRevoke, err, "Failed to revoke work session: %s.", err)
		}

		utils.CliSuccess("Work session %s revoked.", args[0])
		if cleared {
			utils.CliInfo("Active work-session cleared.")
		}
	},
}

// RunRevoke revokes the session and, when it is the workspace's active one, clears it.
// The error is the server's alone; a failed clear only warns. Reports whether it cleared.
func RunRevoke(ac *client.AlpaconClient, id string) (bool, error) {
	if err := wsapi.RevokeWorkSession(ac, id); err != nil {
		return false, err
	}
	return clearEndedActiveSession(ac, id), nil
}
