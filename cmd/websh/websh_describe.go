package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newDescribeCommand(newClient clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:     "describe SESSION_ID",
		Aliases: []string{"desc"},
		Short:   "Show details of a Websh session",
		Example: `  alpacon websh describe 550e8400-e29b-41d4-a716-446655440000
  alpacon websh desc 550e8400-e29b-41d4-a716-446655440000`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			detail, err := websh.GetSessionDetail(alpaconClient, sessionID)
			if err != nil {
				return utils.NewExitError("Failed to retrieve websh session details: %s.", err)
			}

			return utils.PrintJson(detail)
		}),
	}
}
