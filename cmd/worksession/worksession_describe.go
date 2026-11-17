package worksession

import (
	"fmt"
	"strings"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type describeRow struct {
	Field string `table:"Field"`
	Value string `table:"Value"`
}

var workSessionDescribeCmd = newWorkSessionDescribeCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newWorkSessionDescribeCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:     "describe SESSION_ID",
		Aliases: []string{"desc"},
		Short:   "Show details of a work session",
		Args:    cobra.ExactArgs(1),
		Example: `  alpacon work-session describe 550e8400-e29b-41d4-a716-446655440000
  alpacon work-session desc 550e8400-e29b-41d4-a716-446655440000`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			ac, err := newClient()
			if err != nil {
				return utils.NewEnvelopeExitError(opDescribe, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			if ios.IsJSON() {
				body, err := wsapi.GetWorkSessionRaw(ac, args[0])
				if err != nil {
					return utils.NewEnvelopeExitError(opDescribe, err, "Failed to retrieve work session: %s.", err)
				}
				return ios.PrintJson(body)
			}

			session, err := wsapi.GetWorkSession(ac, args[0])
			if err != nil {
				return utils.NewEnvelopeExitError(opDescribe, err, "Failed to retrieve work session: %s.", err)
			}

			createdBy := ""
			if session.CreatedBy != nil {
				createdBy = session.CreatedBy.Name
			}
			assignedUser := ""
			if session.AssignedUser != nil {
				assignedUser = session.AssignedUser.Name
			}
			startedAt := ""
			if session.StartedAt != nil {
				startedAt = session.StartedAt.Local().Format("2006-01-02 15:04")
			}
			completedAt := ""
			if session.CompletedAt != nil {
				completedAt = session.CompletedAt.Local().Format("2006-01-02 15:04")
			}

			rows := []describeRow{
				{"ID", session.ID},
				{"Description", session.Description},
				{"Status", session.Status},
				{"Requester type", session.RequesterType},
				{"Scopes", strings.Join(session.Scopes, ", ")},
				{"Servers", wsapi.ServerList(session)},
				{"Clusters", wsapi.ClusterList(session)},
				{"Created by", createdBy},
				{"Assigned user", assignedUser},
				{"Expires at", wsapi.FormatExpiresAt(session.ExpiresAt)},
				{"Started at", startedAt},
				{"Completed at", completedAt},
				{"Added at", session.AddedAt.Local().Format("2006-01-02 15:04")},
			}

			if err := ios.PrintTable(rows); err != nil {
				return err
			}

			if block := formatAdjustments(session.Adjustments); block != "" {
				fmt.Fprintf(ios.Out, "\nAdjustments:\n%s\n", block)
			}
			if block := formatRecommendations(session.Recommendations); block != "" {
				fmt.Fprintf(ios.Out, "\nRecommendations:\n%s\n", block)
			}
			return nil
		}),
	}
}
