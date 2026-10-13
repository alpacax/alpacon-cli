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
	Long: `Show the recording of one Websh terminal in a work session.

--index counts the session's terminals in timeline order, starting at 1. Only the
chosen terminal's recording is downloaded. Tunnels are not terminals and are not counted.`,
	Args: cobra.ExactArgs(1),
	Example: `  alpacon work-session recording ses-abc123
  alpacon work-session recording ses-abc123 --index 2
  alpacon work-session rec ses-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opRecording, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		sessionID := args[0]

		// Only the terminal list is read whole; the recording itself comes from the
		// per-session records route, so one recording costs one session's bytes.
		terminals, err := wsapi.ListTerminalSessionIDs(ac, sessionID)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opRecording, err, "Failed to retrieve work session timeline: %s.", err)
		}

		// No terminals is a not-found state (no error_code); a bad --index is a usage_error.
		if len(terminals) == 0 {
			utils.CliErrorEnvelopeWithExit(opRecording, nil, "No recordings found for session %s.", sessionID)
		}

		if recordingIndex < 1 || recordingIndex > len(terminals) {
			utils.CliUsageErrorEnvelopeWithExit(opRecording, "Recording index %d out of range (session has %d recording(s)).", recordingIndex, len(terminals))
		}

		record, addedAt, err := wsapi.GetTerminalRecording(ac, sessionID, terminals[recordingIndex-1])
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opRecording, err, "Failed to retrieve recording: %s.", err)
		}
		if record == "" && addedAt == "" {
			utils.CliErrorEnvelopeWithExit(opRecording, nil, "No recordings found for session %s.", sessionID)
		}

		target := &wsapi.TimelineItem{Type: "websh_record", Timestamp: &addedAt}
		printRecordingHeader(os.Stdout, target, recordingIndex, len(terminals))
		printRecordingContent(os.Stdout, record)
	},
}

func init() {
	workSessionRecordingCmd.Flags().IntVar(&recordingIndex, "index", 1, "Terminal to display (1-based, in timeline order)")
}

func printRecordingHeader(w io.Writer, target *wsapi.TimelineItem, idx, total int) {
	header := fmt.Sprintf("Recording %d/%d", idx, total)
	// formatTimestamp hands back the server's string whenever it fails to parse, so
	// this line is a sink for whatever was stored.
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
