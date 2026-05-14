package worksession

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var noRecords bool

type recordingJSON struct {
	Index   int    `json:"index"`
	Time    string `json:"time"`
	Server  string `json:"server"`
	Preview string `json:"preview"`
}

var workSessionTimelineCmd = &cobra.Command{
	Use:     "timeline SESSION_ID",
	Aliases: []string{"tl"},
	Short:   "Show the activity timeline for a work session",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session timeline ses-abc123
  alpacon work-session timeline ses-abc123 --no-records
  alpacon work-session tl ses-abc123 --output json`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		id := args[0]

		var (
			items       []wsapi.TimelineItem
			session     *wsapi.WorkSession
			timelineErr error
			wg          sync.WaitGroup
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			items, timelineErr = wsapi.GetWorkSessionTimeline(ac, id, !noRecords)
		}()
		go func() {
			defer wg.Done()
			session, _ = wsapi.GetWorkSession(ac, id) // best-effort: server names degrade to IDs on failure
		}()
		wg.Wait()

		if timelineErr != nil {
			utils.CliErrorWithExit("Failed to retrieve work session timeline: %s.", timelineErr)
		}

		serverMap := map[string]string{}
		if session != nil {
			for _, s := range session.Servers {
				serverMap[s.ID] = s.Name
			}
		}

		recordingsBySession, recordings := buildRecordingIndex(items)

		var rows []wsapi.TimelineAttributes
		for i := range items {
			if items[i].Type == "websh_record" {
				continue
			}
			row := projectTimelineAttributes(&items[i], serverMap)
			if items[i].Type == "websh_session" {
				if recs := recordingsBySession[items[i].ID]; len(recs) > 0 {
					badge := recordingBadge(len(recs))
					if row.Details != "" {
						row.Details += " " + badge
					} else {
						row.Details = badge
					}
				}
			}
			rows = append(rows, row)
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			var recList []wsapi.TimelineItem
			if !noRecords {
				recList = recordings
			}
			outputTimelineJSON(rows, recList, serverMap)
			return
		}

		utils.PrintTable(rows)
		if !noRecords && len(recordings) > 0 {
			printRecordingsSection(recordings, serverMap)
		}
	},
}

func init() {
	workSessionTimelineCmd.Flags().BoolVar(&noRecords, "no-records", false, "Hide the recordings section below the timeline")
}

func buildRecordingIndex(items []wsapi.TimelineItem) (bySession map[string][]wsapi.TimelineItem, flat []wsapi.TimelineItem) {
	bySession = map[string][]wsapi.TimelineItem{}
	for _, item := range items {
		if item.Type == "websh_record" {
			bySession[item.SessionID] = append(bySession[item.SessionID], item)
			flat = append(flat, item)
		}
	}
	return
}

func recordingBadge(n int) string {
	if n == 1 {
		return "• 1 recording"
	}
	return fmt.Sprintf("• %d recordings", n)
}

func recordingPreview(raw string) string {
	for _, line := range strings.SplitN(raw, "\n", 50) {
		line = ansiEscape.ReplaceAllString(line, "")
		// \r moves cursor to line start; take only the last overwritten segment
		if idx := strings.LastIndex(line, "\r"); idx != -1 {
			line = line[idx+1:]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			return utils.TruncateString(line, 60)
		}
	}
	return ""
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

func printRecordingsSection(recordings []wsapi.TimelineItem, serverMap map[string]string) {
	fmt.Printf("\n─── Recordings (%d) %s\n", len(recordings), strings.Repeat("─", 44))
	for i, rec := range recordings {
		fmt.Printf("[%d]  %s  %s\n", i+1, resolveTimestamp(rec.Timestamp), resolveServer(rec.ServerID, serverMap))
		if preview := recordingPreview(rec.MaskedRecord); preview != "" {
			fmt.Printf("     %s\n", preview)
		}
		if i < len(recordings)-1 {
			fmt.Println()
		}
	}
}

func outputTimelineJSON(rows []wsapi.TimelineAttributes, recordings []wsapi.TimelineItem, serverMap map[string]string) {
	recEntries := make([]recordingJSON, len(recordings))
	for i, rec := range recordings {
		recEntries[i] = recordingJSON{
			Index:   i + 1,
			Time:    resolveTimestamp(rec.Timestamp),
			Server:  resolveServer(rec.ServerID, serverMap),
			Preview: recordingPreview(rec.MaskedRecord),
		}
	}
	out := map[string]any{
		"timeline":   rows,
		"recordings": recEntries,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		utils.CliErrorWithExit("Failed to serialize timeline: %s.", err)
	}
	fmt.Println(string(b))
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
	date, rest, found := strings.Cut(ts, "T")
	if !found {
		return ts
	}
	if idx := strings.IndexAny(rest, ".+Z"); idx != -1 {
		rest = rest[:idx]
	}
	return date + " " + rest
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
	case "websh_record":
		return "recording"
	default:
		return t
	}
}

func formatDetails(item *wsapi.TimelineItem) string {
	switch item.Type {
	case "command":
		status := "ok"
		if item.Denied {
			status = "denied"
		} else if item.Success != nil && !*item.Success {
			status = "failed"
		}
		return fmt.Sprintf("[%s] %s", status, utils.TruncateString(item.Line, 60))

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
		return fmt.Sprintf("↑ %s (%s)", item.Name, formatSize(item.Size))

	case "file_download":
		return fmt.Sprintf("↓ %s (%s)", item.Name, formatSize(item.Size))

	case "sudo_grant":
		detail := fmt.Sprintf("%s: %s", item.GrantType, item.Status)
		if item.Command != nil && *item.Command != "" {
			detail += fmt.Sprintf(" — %s", utils.TruncateString(*item.Command, 40))
		}
		return detail

	case "websh_record":
		return utils.TruncateString(item.MaskedRecord, 60)

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
