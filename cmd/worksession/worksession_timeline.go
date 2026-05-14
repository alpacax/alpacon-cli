package worksession

import (
	"fmt"
	"strings"
	"sync"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var noRecords bool

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
			session, _ = wsapi.GetWorkSession(ac, id)
		}()
		wg.Wait()

		if timelineErr != nil {
			utils.CliErrorWithExit("Failed to retrieve work session timeline: %s.", timelineErr)
		}

		// Build server name map from session detail for human-readable server names.
		serverMap := map[string]string{}
		if session != nil {
			for _, s := range session.Servers {
				serverMap[s.ID] = s.Name
			}
		}

		rows := make([]wsapi.TimelineAttributes, len(items))
		for i := range items {
			rows[i] = projectTimelineAttributes(&items[i], serverMap)
		}
		utils.PrintTable(rows)
	},
}

func init() {
	workSessionTimelineCmd.Flags().BoolVar(&noRecords, "no-records", false, "Exclude Websh session recordings from the timeline")
}

func projectTimelineAttributes(item *wsapi.TimelineItem, serverMap map[string]string) wsapi.TimelineAttributes {
	ts := ""
	if item.Timestamp != nil {
		ts = formatTimestamp(*item.Timestamp)
	}

	server := ""
	if item.ServerID != nil {
		if name, ok := serverMap[*item.ServerID]; ok {
			server = name
		} else {
			server = *item.ServerID
		}
	}

	return wsapi.TimelineAttributes{
		Time:    ts,
		Type:    formatType(item.Type),
		Server:  server,
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
	case "command":
		return "command"
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
