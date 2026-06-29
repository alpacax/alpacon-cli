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

func describeRows(session *wsapi.WorkSession) []describeRow {
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
	}
	adj := session.Adjustments
	if adj != nil && adj.Scopes != nil {
		rows = append(rows, describeRow{"Scopes adjusted", formatScopeDiff(adj.Scopes)})
	}
	rows = append(rows, describeRow{"Servers", strings.Join(serverNames, ", ")})
	if adj != nil && adj.Servers != nil {
		rows = append(rows, describeRow{"Servers adjusted", formatServerDiff(adj.Servers)})
	}
	rows = append(rows,
		describeRow{"Created by", createdBy},
		describeRow{"Assigned user", assignedUser},
		describeRow{"Expires at", session.ExpiresAt.Local().Format("2006-01-02 15:04")},
		describeRow{"Started at", startedAt},
		describeRow{"Completed at", completedAt},
		describeRow{"Added at", session.AddedAt.Local().Format("2006-01-02 15:04")},
	)
	if len(session.Recommendations) > 0 {
		recs := make([]string, len(session.Recommendations))
		for i, r := range session.Recommendations {
			recs[i] = formatRecommendation(r)
		}
		rows = append(rows, describeRow{"Recommendations", strings.Join(recs, "; ")})
	}
	return rows
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

		utils.PrintTable(describeRows(session))
	},
}
