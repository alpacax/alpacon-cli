package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionRevokeCmd = newWorkSessionRevokeCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newWorkSessionRevokeCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:     "revoke SESSION_ID",
		Short:   "Force-terminate a work session",
		Long:    "Force-terminate an active or approved work session. Superuser only.",
		Args:    cobra.ExactArgs(1),
		Example: `  alpacon work-session revoke 550e8400-e29b-41d4-a716-446655440000`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			ac, err := newClient()
			if err != nil {
				return utils.NewEnvelopeExitError(opRevoke, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			cleared, err := RunRevoke(ios, ac, args[0])
			if err != nil {
				return utils.NewEnvelopeExitError(opRevoke, err, "Failed to revoke work session: %s.", err)
			}

			ios.Success("Work session %s revoked.", args[0])
			if cleared {
				ios.Info("Active work-session cleared.")
			}
			return nil
		}),
	}
}

// RunRevoke revokes the session; see endSession.
func RunRevoke(ios *utils.IOStreams, ac *client.AlpaconClient, id string) (bool, error) {
	return endSession(ios, ac, id, wsapi.RevokeWorkSession)
}
