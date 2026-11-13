package worksession

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"unicode/utf8"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// recordingJSON is one Websh terminal, numbered as `recording --index` counts them.
type recordingJSON struct {
	Index             int             `json:"index"`
	WebshSessionID    string          `json:"websh_session_id"`
	Time              utils.Timestamp `json:"time"`
	Server            string          `json:"server"`
	RecordChunksTotal *int            `json:"record_chunks_total"`
	Status            string          `json:"status"`
}

var noRecords bool

var workSessionTimelineCmd = &cobra.Command{
	Use:     "timeline SESSION_ID",
	Aliases: []string{"tl"},
	Short:   "Show the activity timeline for a work session",
	Long: `Show the activity timeline for a work session.

Recordings are not embedded in the timeline. The recordings section lists each
Websh terminal with the number of recording chunks it has ("unknown" when the
server cannot count them) and the command that shows one:
alpacon work-session recording SESSION_ID --index N. --no-records leaves the
section out.`,
	Args: cobra.ExactArgs(1),
	Example: `  alpacon work-session timeline 550e8400-e29b-41d4-a716-446655440000
  alpacon work-session timeline 550e8400-e29b-41d4-a716-446655440000 --no-records
  alpacon work-session tl 550e8400-e29b-41d4-a716-446655440000 --output json
  alpacon work-session recording 550e8400-e29b-41d4-a716-446655440000 --index 2`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewEnvelopeExitError(opTimeline, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		id := args[0]

		var (
			items       []wsapi.TimelineItem
			session     *wsapi.WorkSession
			timelineErr error
			wg          sync.WaitGroup
		)
		wg.Go(func() {
			items, timelineErr = wsapi.GetWorkSessionTimeline(ac, id)
		})
		wg.Go(func() {
			session, _ = wsapi.GetWorkSession(ac, id) // best-effort: server names degrade to IDs on failure
		})
		wg.Wait()

		if timelineErr != nil {
			return utils.NewEnvelopeExitError(opTimeline, timelineErr, "Failed to retrieve work session timeline: %s.", timelineErr)
		}

		serverMap := map[string]string{}
		if session != nil {
			for _, s := range session.Servers {
				serverMap[s.ID] = s.Name
			}
		}

		rows := make([]wsapi.TimelineAttributes, 0, len(items))
		var terminals []wsapi.TimelineItem
		for i := range items {
			// A server too old to know include_records sends the recordings
			// anyway; they are not timeline rows.
			if items[i].Type == "websh_record" {
				continue
			}
			if !noRecords && wsapi.IsTerminal(&items[i]) {
				terminals = append(terminals, items[i])
			}
			rows = append(rows, projectTimelineAttributes(&items[i], serverMap))
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			return outputTimelineJSON(rows, terminals, id, serverMap)
		}

		writer, cleanup := utils.WriteToPager()
		defer cleanup()
		printTimelineTableTo(writer, rows)
		if len(terminals) > 0 {
			printRecordingsSectionTo(writer, terminals, id, serverMap)
		}
		return nil
	}),
}

func init() {
	workSessionTimelineCmd.Flags().BoolVar(&noRecords, "no-records", false,
		"Hide the recordings section (an empty recordings list in JSON)")
}

func recordingStatus(total *int) string {
	switch {
	case total == nil || *total < 0:
		return "unknown"
	case *total == 0:
		return "no recording"
	case *total == 1:
		return "1 chunk"
	default:
		return fmt.Sprintf("%d chunks", *total)
	}
}

func recordingCommand(sessionID string, index int) string {
	return fmt.Sprintf("alpacon work-session recording %s --index %d", sessionID, index)
}

func resolveTimestamp(ts *string) utils.Timestamp {
	if ts == nil {
		return utils.Timestamp{}
	}
	return utils.ParseLocalTimestamp(*ts)
}

func resolveServer(serverID *string, serverMap map[string]string) string {
	if serverID == nil {
		return ""
	}
	if name, ok := serverMap[*serverID]; ok {
		return name
	}
	return *serverID
}

// The rows bypass utils.PrintTable, so the sanitizing pass every other table
// gets has to happen here. Server and user names are set by whoever registered
// them, and a control sequence in a column erases the audit line it sits on.
func printTimelineTableTo(w io.Writer, rows []wsapi.TimelineAttributes) {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TIME\tTYPE\tSERVER\tUSER\tDETAILS")
	for _, row := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			utils.SanitizeTerminalText(row.Time.String()),
			utils.SanitizeTerminalText(row.Type),
			utils.SanitizeTerminalText(row.Server),
			utils.SanitizeTerminalText(row.User),
			utils.SanitizeTerminalText(row.Details.String()))
	}
	_ = tw.Flush()
}

// printRecordingsSectionTo numbers the terminals as --index takes them, and gives no hint to a
// terminal known to have no recording.
func printRecordingsSectionTo(w io.Writer, terminals []wsapi.TimelineItem, sessionID string, serverMap map[string]string) {
	label := "terminals"
	if len(terminals) == 1 {
		label = "terminal"
	}
	_, _ = fmt.Fprintf(w, "\n─── Recordings (%d %s) %s\n", len(terminals), label, strings.Repeat("─", 40))
	for i := range terminals {
		term := &terminals[i]
		_, _ = fmt.Fprintf(w, "[%d]  %s  %s  %s\n", i+1,
			utils.SanitizeTerminalText(resolveTimestamp(term.Timestamp).String()),
			utils.SanitizeTerminalText(resolveServer(term.ServerID, serverMap)),
			recordingStatus(term.RecordChunksTotal))
		if total := term.RecordChunksTotal; total == nil || *total != 0 {
			_, _ = fmt.Fprintf(w, "     %s\n", utils.SanitizeTerminalText(recordingCommand(sessionID, i+1)))
		}
	}
}

func outputTimelineJSON(rows []wsapi.TimelineAttributes, terminals []wsapi.TimelineItem, sessionID string, serverMap map[string]string) error {
	recEntries := make([]recordingJSON, len(terminals))
	for i := range terminals {
		term := &terminals[i]
		recEntries[i] = recordingJSON{
			Index:             i + 1,
			WebshSessionID:    term.ID,
			Time:              resolveTimestamp(term.Timestamp),
			Server:            resolveServer(term.ServerID, serverMap),
			RecordChunksTotal: term.RecordChunksTotal,
			Status:            recordingStatus(term.RecordChunksTotal),
		}
	}
	out := map[string]any{
		"timeline":   rows,
		"recordings": recEntries,
	}
	// Not a plain Marshal: the shared writer escapes the format and C1 runes that
	// encoding/json leaves alone, and this output is read in a terminal too.
	if err := utils.PrintJSONValue(os.Stdout, out); err != nil {
		return utils.NewEnvelopeExitError(opTimeline, err, "Failed to serialize timeline: %s.", err)
	}
	return nil
}

func projectTimelineAttributes(item *wsapi.TimelineItem, serverMap map[string]string) wsapi.TimelineAttributes {
	return wsapi.TimelineAttributes{
		ID:                item.ID,
		Time:              resolveTimestamp(item.Timestamp),
		Type:              formatType(item.Type),
		Server:            resolveServer(item.ServerID, serverMap),
		User:              item.Username,
		Details:           formatDetails(item),
		CommandRejection:  item.CommandRejection,
		SudoRefusalReason: item.SudoRefusalReason,
	}
}

func formatTimestamp(ts string) string {
	return utils.FormatTimestamp(ts)
}

func formatType(t string) string {
	switch t {
	case "websh_session":
		return "websh"
	case "tunnel_session":
		return "tunnel"
	case "ftp_session":
		return "ftp"
	case "file_upload":
		return "upload"
	case "file_download":
		return "download"
	case "sudo_grant":
		return "sudo grant"
	default:
		return t
	}
}

// formatDetails cuts only the command text in a table; JSON gets it whole.
func formatDetails(item *wsapi.TimelineItem) utils.TruncatedText {
	switch item.Type {
	case "command":
		status := "unknown"
		if item.IsRejected() {
			status = item.Label()
		} else if item.Denied {
			status = "denied"
		} else if item.SudoRefusalReason.Value != "" {
			// The command was allowed to run but the host refused a sudo inside it,
			// so its exit status alone would read as an ordinary failure.
			status = "sudo denied: " + utils.SanitizeTerminalText(item.SudoRefusalReason.Value)
		} else if item.Success != nil {
			if *item.Success {
				status = "ok"
			} else {
				status = "failed"
			}
		}
		return truncatedAfter(fmt.Sprintf("[%s] ", status), utils.SanitizeTerminalText(item.Line), 60)

	case "websh_session":
		state := sessionState(item.ClosedAt)
		if item.ClientType != "" {
			return whole(fmt.Sprintf("%s (client: %s)", state, item.ClientType))
		}
		return whole(state)

	case "tunnel_session":
		state := sessionState(item.ClosedAt)
		if item.TargetPort != nil {
			return whole(fmt.Sprintf("port %d %s", *item.TargetPort, state))
		}
		return whole(state)

	case "ftp_session":
		return whole(sessionState(item.ClosedAt))

	case "file_upload":
		return whole(fmt.Sprintf("↑ %s (%s)", utils.SanitizeTerminalText(item.Name), formatSize(item.Size)))

	case "file_download":
		return whole(fmt.Sprintf("↓ %s (%s)", utils.SanitizeTerminalText(item.Name), formatSize(item.Size)))

	case "sudo_grant":
		detail := fmt.Sprintf("%s: %s", item.GrantType, item.Status)
		if item.Command != nil && *item.Command != "" {
			return truncatedAfter(detail+" — ", utils.SanitizeTerminalText(*item.Command), 40)
		}
		return whole(detail)

	default:
		return whole("")
	}
}

func truncatedAfter(prefix, text string, width int) utils.TruncatedText {
	return utils.Truncate(prefix+text, utf8.RuneCountInString(prefix)+width)
}

func whole(s string) utils.TruncatedText {
	return utils.Truncate(s, utf8.RuneCountInString(s))
}

func sessionState(closedAt *string) string {
	if closedAt != nil {
		return "closed"
	}
	return "opened"
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
