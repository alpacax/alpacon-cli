package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newListCommand(newClient clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List active Websh sessions",
		Example: `  alpacon websh ls
  alpacon websh ls --tail 50`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			tail, _ := cmd.Flags().GetInt("tail")
			if err := utils.CheckPositiveInt("tail", tail); err != nil {
				return err
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			sessionList, err := websh.GetSessionList(alpaconClient, tail)
			if err != nil {
				return utils.NewExitError("Failed to retrieve websh sessions: %s.", err)
			}

			utils.PrintTable(sessionList)
			return nil
		}),
	}
	cmd.Flags().IntP("tail", "t", 25, "Number of sessions to show, newest first")
	return cmd
}
