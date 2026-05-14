package worksession

import (
	"fmt"
	"regexp"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// ansiEscape matches ANSI/VT escape sequences: CSI, OSC, and single-char Fe sequences.
var ansiEscape = regexp.MustCompile(`\x1b(?:\][^\x07]*\x07|\[[0-9;?]*[A-Za-z]|[@-Z\\-_])`)

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
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		sessionID := args[0]

		items, err := wsapi.GetWorkSessionTimeline(ac, sessionID, true)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve work session timeline: %s.", err)
		}

		var recordings []wsapi.TimelineItem
		for _, item := range items {
			if item.Type == "websh_record" {
				recordings = append(recordings, item)
			}
		}

		if len(recordings) == 0 {
			utils.CliErrorWithExit("No recordings found for session %s.", sessionID)
		}

		target, idx := findRecording(recordings, recordingIndex)
		if target == nil {
			utils.CliErrorWithExit("Recording index %d out of range (session has %d recording(s)).", recordingIndex, len(recordings))
		}

		printRecordingHeader(target, idx, len(recordings))
		printRecordingContent(target.MaskedRecord)
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

func printRecordingHeader(target *wsapi.TimelineItem, idx int, total int) {
	header := fmt.Sprintf("Recording %d/%d", idx, total)
	if ts := resolveTimestamp(target.Timestamp); ts != "" {
		header += " — " + ts
	}
	fmt.Println(header)
	fmt.Println()
}

func printRecordingContent(raw string) {
	content := ansiEscape.ReplaceAllString(raw, "")
	fmt.Print(content)
	if len(content) > 0 && content[len(content)-1] != '\n' {
		fmt.Println()
	}
}
