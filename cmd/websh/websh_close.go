package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newCloseCommand(newClient clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:     "close SESSION_ID",
		Short:   "Close a Websh session",
		Example: `  alpacon websh close 550e8400-e29b-41d4-a716-446655440000`,
		Args:    cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			err = websh.CloseSession(alpaconClient, sessionID)
			if err != nil {
				return utils.NewExitError("Failed to close websh session: %s.", err)
			}

			utils.CliSuccess("Session '%s' has been closed.", sessionID)
			return nil
		}),
	}
}
