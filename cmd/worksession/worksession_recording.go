package worksession

import (
	"fmt"
	"io"
	"os"
	"strings"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var recordingIndex int

var workSessionRecordingCmd = &cobra.Command{
	Use:     "recording SESSION_ID",
	Aliases: []string{"rec"},
	Short:   "Show a Websh session recording",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon work-session recording ses-abc123
  alpacon work-session recording ses-abc123 --index 2
  alpacon work-session rec ses-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opRecording, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		sessionID := args[0]

		items, err := wsapi.GetWorkSessionTimeline(ac, sessionID, true)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opRecording, err, "Failed to retrieve work session timeline: %s.", err)
		}

		var recordings []wsapi.TimelineItem
		for _, item := range items {
			if item.Type == "websh_record" {
				recordings = append(recordings, item)
			}
		}

		// No recordings is a not-found state (no error_code); a bad --index is a usage_error.
		if len(recordings) == 0 {
			utils.CliErrorEnvelopeWithExit(opRecording, nil, "No recordings found for session %s.", sessionID)
		}

		target, idx := findRecording(recordings, recordingIndex)
		if target == nil {
			utils.CliUsageErrorEnvelopeWithExit(opRecording, "Recording index %d out of range (session has %d recording(s)).", recordingIndex, len(recordings))
		}

		printRecordingHeader(os.Stdout, target, idx, len(recordings))
		printRecordingContent(os.Stdout, target.MaskedRecord)
	},
}

func init() {
	workSessionRecordingCmd.Flags().IntVar(&recordingIndex, "index", 1, "Recording index to display (1-based)")
}

func findRecording(recordings []wsapi.TimelineItem, index int) (*wsapi.TimelineItem, int) {
	if index < 1 || index > len(recordings) {
		return nil, -1
	}
	return &recordings[index-1], index
}

func printRecordingHeader(w io.Writer, target *wsapi.TimelineItem, idx, total int) {
	header := fmt.Sprintf("Recording %d/%d", idx, total)
	// formatTimestamp hands back the server's string whenever it fails to parse, so
	// this line is a sink for whatever was stored, same as every other timeline cell.
	if ts := utils.SanitizeTerminalText(resolveTimestamp(target.Timestamp)); ts != "" {
		header += " — " + ts
	}
	_, _ = fmt.Fprintln(w, header)
	_, _ = fmt.Fprintln(w)
}

func printRecordingContent(w io.Writer, raw string) {
	// No full control pass: this path shows the recording as it was, so \r and the
	// line endings have to survive. ansiEscapeRE matches ESC-led forms ending in
	// \x40-\x7e alone, so "ESC ( 0" and "ESC 7" get past it and switch the charset
	// or move the cursor off the line. By here every matched sequence is gone, so a
	// leftover ESC is an introducer that failed to match and its tail belongs on
	// screen as text. SO and SI reach that same charset switch without an ESC and
	// hold it until a reset; xterm in UTF-8 mode ignores them, the Linux console
	// and VTE do not.
	content := strings.Map(func(r rune) rune {
		if r == 0x1b || r == 0x0e || r == 0x0f || utils.IsC1OrDEL(r) {
			return -1
		}
		return r
	}, utils.StripFormatAndANSI(raw))
	_, _ = fmt.Fprint(w, content)
	if len(content) > 0 && content[len(content)-1] != '\n' {
		_, _ = fmt.Fprintln(w)
	}
}
