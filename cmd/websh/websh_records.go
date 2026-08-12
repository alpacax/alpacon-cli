package websh

import (
	"strings"

	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webshRecordsCmd = &cobra.Command{
	Use:     "records [flags] SESSION_ID",
	Aliases: []string{"rec"},
	Short:   "Retrieve or search records within a Websh session",
	Long: `Retrieve masked terminal records for a Websh session, optionally
filtering by command text. Available on paid plans only.

Use --query to search records by command text (fuzzy match).`,
	Example: `  alpacon websh records abc123
  alpacon websh records abc123 --query docker
  alpacon websh rec abc123 -q "sudo reboot" -n 50`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sessionID := args[0]
		query, _ := cmd.Flags().GetString("query")
		limit, _ := cmd.Flags().GetInt("limit")
		utils.RequirePositiveInt("limit", limit)

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		records, err := websh.GetSessionRecords(alpaconClient, sessionID, query, limit)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve Websh session records: %s.", err)
		}

		// JSON keeps records verbatim; table sanitizes so terminal control chars don't break the layout.
		if utils.OutputFormat == utils.OutputFormatJSON {
			utils.PrintTable(records)
			return
		}

		display := make([]websh.SessionRecord, len(records))
		for i, r := range records {
			display[i] = websh.SessionRecord{
				AddedAt: r.AddedAt,
				Record:  sanitizeRecord(r.Record, 80),
			}
		}
		utils.PrintTable(display)
	},
}

func sanitizeRecord(record string, width int) string {
	// Format chars are deleted rather than spaced: a space would be
	// indistinguishable from whitespace that was actually recorded, and no Cf rune
	// carries anything a reviewer needs. The tradeoff is that legitimate text
	// changes shape — a ZWJ emoji splits into its parts, Arabic joining breaks.
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

func init() {
	webshRecordsCmd.Flags().StringP("query", "q", "", "Search records by command text (fuzzy match)")
	webshRecordsCmd.Flags().IntP("limit", "n", 100, "Maximum number of records to fetch")
}
