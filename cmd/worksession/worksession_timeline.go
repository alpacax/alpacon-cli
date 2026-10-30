package worksession

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"text/tabwriter"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// recordingJSON is one Websh terminal of the timeline, numbered as
// `recording --index` counts them. RecordChunksTotal is null when the server
// could not count the chunks.
type recordingJSON struct {
	Index             int    `json:"index"`
	SessionID         string `json:"session_id"`
	Time              string `json:"time"`
	Server            string `json:"server"`
	RecordChunksTotal *int   `json:"record_chunks_total"`
	Status            string `json:"status"`
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
	Example: `  alpacon work-session timeline ses-abc123
  alpacon work-session timeline ses-abc123 --no-records
  alpacon work-session tl ses-abc123 --output json
  alpacon work-session recording ses-abc123 --index 2`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opTimeline, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
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
			utils.CliErrorEnvelopeWithExit(opTimeline, timelineErr, "Failed to retrieve work session timeline: %s.", timelineErr)
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
			outputTimelineJSON(rows, terminals, id, serverMap)
			return
		}

		writer, cleanup := utils.WriteToPager()
		defer cleanup()
		printTimelineTableTo(writer, rows)
		if len(terminals) > 0 {
			printRecordingsSectionTo(writer, terminals, id, serverMap)
		}
	},
}

func init() {
	workSessionTimelineCmd.Flags().BoolVar(&noRecords, "no-records", false,
		"Hide the recordings section (an empty recordings list in JSON)")
}

// recordingStatus reads a terminal's record_chunks_total. nil covers both a
// server that could not count (null) and one that predates the count (no key).
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

func resolveTimestamp(ts *string) string {
	if ts == nil {
		return ""
	}
	return formatTimestamp(*ts)
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
			utils.SanitizeTerminalText(row.Time),
			utils.SanitizeTerminalText(row.Type),
			utils.SanitizeTerminalText(row.Server),
			utils.SanitizeTerminalText(row.User),
			utils.SanitizeTerminalText(row.Details))
	}
	_ = tw.Flush()
}

// printRecordingsSectionTo lists the terminals in timeline order, so [N] is the
// --index that reads that terminal's recording. A terminal known to have no
// recording gets no hint, since the command would only report that.
func printRecordingsSectionTo(w io.Writer, terminals []wsapi.TimelineItem, sessionID string, serverMap map[string]string) {
	label := "terminals"
	if len(terminals) == 1 {
		label = "terminal"
	}
	_, _ = fmt.Fprintf(w, "\n─── Recordings (%d %s) %s\n", len(terminals), label, strings.Repeat("─", 40))
	for i := range terminals {
		term := &terminals[i]
		_, _ = fmt.Fprintf(w, "[%d]  %s  %s  %s\n", i+1,
			utils.SanitizeTerminalText(resolveTimestamp(term.Timestamp)),
			utils.SanitizeTerminalText(resolveServer(term.ServerID, serverMap)),
			recordingStatus(term.RecordChunksTotal))
		if total := term.RecordChunksTotal; total == nil || *total != 0 {
			_, _ = fmt.Fprintf(w, "     %s\n", utils.SanitizeTerminalText(recordingCommand(sessionID, i+1)))
		}
	}
}

func outputTimelineJSON(rows []wsapi.TimelineAttributes, terminals []wsapi.TimelineItem, sessionID string, serverMap map[string]string) {
	recEntries := make([]recordingJSON, len(terminals))
	for i := range terminals {
		term := &terminals[i]
		recEntries[i] = recordingJSON{
			Index:             i + 1,
			SessionID:         term.ID,
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
		utils.CliErrorEnvelopeWithExit(opTimeline, err, "Failed to serialize timeline: %s.", err)
	}
}

func projectTimelineAttributes(item *wsapi.TimelineItem, serverMap map[string]string) wsapi.TimelineAttributes {
	return wsapi.TimelineAttributes{
		Time:    resolveTimestamp(item.Timestamp),
		Type:    formatType(item.Type),
		Server:  resolveServer(item.ServerID, serverMap),
		User:    item.Username,
		Details: formatDetails(item),
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

func formatDetails(item *wsapi.TimelineItem) string {
	switch item.Type {
	case "command":
		status := "unknown"
		if item.Denied {
			status = "denied"
		} else if item.Success != nil {
			if *item.Success {
				status = "ok"
			} else {
				status = "failed"
			}
		}
		return fmt.Sprintf("[%s] %s", status, utils.TruncateString(utils.SanitizeTerminalText(item.Line), 60))

	case "websh_session":
		state := sessionState(item.ClosedAt)
		if item.ClientType != "" {
			return fmt.Sprintf("%s (client: %s)", state, item.ClientType)
		}
		return state

	case "tunnel_session":
		state := sessionState(item.ClosedAt)
		if item.TargetPort != nil {
			return fmt.Sprintf("port %d %s", *item.TargetPort, state)
		}
		return state

	case "ftp_session":
		return sessionState(item.ClosedAt)

	case "file_upload":
		return fmt.Sprintf("↑ %s (%s)", utils.SanitizeTerminalText(item.Name), formatSize(item.Size))

	case "file_download":
		return fmt.Sprintf("↓ %s (%s)", utils.SanitizeTerminalText(item.Name), formatSize(item.Size))

	case "sudo_grant":
		detail := fmt.Sprintf("%s: %s", item.GrantType, item.Status)
		if item.Command != nil && *item.Command != "" {
			detail += fmt.Sprintf(" — %s", utils.TruncateString(utils.SanitizeTerminalText(*item.Command), 40))
		}
		return detail

	default:
		return ""
	}
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
