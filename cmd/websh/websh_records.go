package websh

import (
	"regexp"
	"strings"

	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var whitespaceRunRE = regexp.MustCompile(`\s+`)

var webshRecordsCmd = &cobra.Command{
	Use:     "records SESSION_ID",
	Aliases: []string{"rec"},
	Short:   "Retrieve or search records within a websh session",
	Long: `Retrieve masked terminal records for a websh session, optionally
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
		if limit <= 0 {
			utils.CliErrorWithExit("--limit must be a positive integer.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		records, err := websh.GetSessionRecords(alpaconClient, sessionID, query, limit)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve websh session records: %s.", err)
		}

		// JSON output keeps records verbatim; table output sanitizes the
		// terminal recording so control chars don't break the layout.
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

func sanitizeRecord(s string, width int) string {
	s = utils.StripANSIEscapes(s)
	s = whitespaceRunRE.ReplaceAllString(s, " ")
	s = utils.StripControlChars(s)
	s = strings.TrimSpace(s)
	return utils.TruncateString(s, width)
}
