package worksession

import (
	"fmt"

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
	Example: `  alpacon work-session recording 550e8400-e29b-41d4-a716-446655440000
  alpacon work-session recording 550e8400-e29b-41d4-a716-446655440000 --index 2
  alpacon work-session rec 550e8400-e29b-41d4-a716-446655440000`,
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
	content := utils.StripANSIEscapes(raw)
	fmt.Print(content)
	if len(content) > 0 && content[len(content)-1] != '\n' {
		fmt.Println()
	}
}
