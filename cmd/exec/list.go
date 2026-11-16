package exec

import (
	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newListCommand(newClient clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List recent remote command executions",
		Long: `List recent remote command executions, most recent first.

Use --tail to limit the number of entries, --server to scope to one server,
and --user to filter by the requesting user.`,
		Example: `  alpacon exec ls
  alpacon exec ls --tail 10
  alpacon exec ls --tail 10 --server my-server --user admin`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, _ []string) error {
			return listFromFlags(cmd, newClient)
		}),
	}
	AddListFlags(cmd)
	return cmd
}

// AddListFlags and ListFromFlags are exported for the deprecated 'alpacon event', which
// delegates here. Both live in this file so the flag names exist in exactly one place.
func AddListFlags(cmd *cobra.Command) {
	cmd.Flags().IntP("tail", "t", 25, "Number of command entries to show, newest first")
	cmd.Flags().StringP("server", "s", "", "Filter by server name")
	cmd.Flags().StringP("user", "u", "", "Filter by requesting user")
}

// ListFromFlags requires the flag set registered by AddListFlags.
func ListFromFlags(cmd *cobra.Command) error {
	return listFromFlags(cmd, client.NewAlpaconAPIClient)
}

func listFromFlags(cmd *cobra.Command, newClient clientFactory) error {
	tail, _ := cmd.Flags().GetInt("tail")
	serverName, _ := cmd.Flags().GetString("server")
	userName, _ := cmd.Flags().GetString("user")
	if err := utils.CheckPositiveInt("tail", tail); err != nil {
		return err
	}

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	eventList, err := event.GetEventList(alpaconClient, tail, serverName, userName)
	if err != nil {
		return utils.NewExitError("Failed to retrieve the commands: %s.", err)
	}

	return utils.PrintTable(eventList)
}
