package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newForceCloseCommand(newClient clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:     "force-close SESSION_ID",
		Short:   "Force close a Websh session (superuser only)",
		Example: `  alpacon websh force-close 550e8400-e29b-41d4-a716-446655440000`,
		Args:    cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			err = websh.ForceCloseSession(alpaconClient, sessionID)
			if err != nil {
				return utils.NewExitError("Failed to force close websh session: %s.", err)
			}

			utils.CliSuccess("Session '%s' has been force closed.", sessionID)
			return nil
		}),
	}
}
