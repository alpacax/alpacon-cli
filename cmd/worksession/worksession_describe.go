package worksession

import (
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

var workSessionDescribeCmd = &cobra.Command{
	Use:     "describe SESSION_ID",
	Aliases: []string{"desc"},
	Short:   "Show details of a work session",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session describe ses-abc123
  alpacon work-session desc ses-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opDescribe, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			body, err := wsapi.GetWorkSessionRaw(ac, args[0])
			if err != nil {
				utils.CliErrorEnvelopeWithExit(opDescribe, err, "Failed to retrieve work session: %s.", err)
			}
			utils.PrintJson(body)
			return
		}

		session, err := wsapi.GetWorkSession(ac, args[0])
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opDescribe, err, "Failed to retrieve work session: %s.", err)
		}

		serverNames := make([]string, len(session.Servers))
		for i, s := range session.Servers {
			serverNames[i] = s.Name
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
			{"Servers", strings.Join(serverNames, ", ")},
			{"Created by", createdBy},
			{"Assigned user", assignedUser},
			{"Expires at", session.ExpiresAt.Local().Format("2006-01-02 15:04")},
			{"Started at", startedAt},
			{"Completed at", completedAt},
			{"Added at", session.AddedAt.Local().Format("2006-01-02 15:04")},
		}

		utils.PrintTable(rows)
	},
}
