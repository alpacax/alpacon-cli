package websh

import (
	"strings"

	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newRecordsCommand(newClient clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "records SESSION_ID",
		Aliases: []string{"rec"},
		Short:   "Retrieve or search records within a Websh session",
		Long: `Retrieve masked terminal records for a Websh session, optionally
filtering by command text. Available on paid plans only.

Use --query to search records by command text (fuzzy match).`,
		Example: `  alpacon websh records 550e8400-e29b-41d4-a716-446655440000
  alpacon websh records 550e8400-e29b-41d4-a716-446655440000 --query docker
  alpacon websh rec 550e8400-e29b-41d4-a716-446655440000 -q "sudo reboot" -n 50`,
		Args: cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			query, _ := cmd.Flags().GetString("query")
			limit, _ := cmd.Flags().GetInt("limit")
			if err := utils.CheckPositiveInt("limit", limit); err != nil {
				return err
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			records, err := websh.GetSessionRecords(alpaconClient, sessionID, query, limit)
			if err != nil && len(records) == 0 {
				return utils.NewExitError("Failed to retrieve Websh session records: %s.", err)
			}

			// JSON keeps records verbatim; table sanitizes so terminal control chars don't break the layout.
			rows := records
			if utils.OutputFormat != utils.OutputFormatJSON {
				rows = make([]websh.SessionRecord, len(records))
				for i, r := range records {
					rows[i] = websh.SessionRecord{
						AddedAt: r.AddedAt,
						Record:  sanitizeRecord(r.Record, 80),
					}
				}
			}

			if err != nil {
				return utils.PartialTableExitError(rows, "Showing only the %d records read before the request failed: %s.", len(rows), err)
			}
			utils.PrintTable(rows)
			return nil
		}),
	}
	cmd.Flags().StringP("query", "q", "", "Search records by command text (fuzzy match)")
	cmd.Flags().IntP("limit", "n", 100, "Maximum number of records to fetch")
	return cmd
}

func sanitizeRecord(record string, width int) string {
	// Format chars are deleted rather than spaced: a space would read as recorded whitespace, and
	// no Cf rune carries anything a reviewer needs. The cost: a ZWJ emoji splits, Arabic joining
	// breaks.
	record = utils.StripFormatAndANSI(record)
	// Map control chars to spaces so they separate tokens instead of merging them.
	record = strings.Map(func(r rune) rune {
		if utils.IsControlRune(r) {
			return ' '
		}
		return r
	}, record)
	record = strings.Join(strings.Fields(record), " ")
	return utils.TruncateString(record, width)
}
