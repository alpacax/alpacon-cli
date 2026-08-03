package event

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	eventapi "github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// Covers one provision + dial (watchHandshakeTimeout in api/event) plus the subscribe.
const watchConnectTimeout = 15 * time.Second

var watchCmd = &cobra.Command{
	Use:   "watch --type TYPE [--target TARGET_ID]",
	Short: "Stream events from the Alpacon event channel",
	Long: `Stream events from the Alpacon event channel until interrupted.

One line is written to stdout per event; everything else goes to stderr, so the
stream stays machine-readable when piped. With --output json each line is the
server frame compacted to a single line (NDJSON)—whitespace is dropped, but
every field and the server's key order survive, including fields this CLI does
not know. The default table format prints four fixed fields: receive time,
event type, sub type, and the target given by --target.

The connection is re-established automatically. Events published while
disconnected are lost—the event channel has no history to replay—and both
reconnects and failing reconnects are reported on stderr.

Run 'alpacon work-session ls' or the Alpacon console to find a target ID.`,
	Example: `  alpacon event watch --type work_session --target a1b2c3d4-5678-abcd-ef01-234567890abc
  alpacon event watch --type work_session --target a1b2c3d4-5678-abcd-ef01-234567890abc --output json
  alpacon event watch --type notification`,
	Args: cobra.NoArgs,
	Run:  runWatch,
}

func init() {
	watchCmd.Flags().String("type", "", "Event type to subscribe to, e.g. work_session or notification (required)")
	watchCmd.Flags().String("target", "", "Target resource ID; omit where the server allows a subscription without one")

	EventCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, _ []string) {
	eventType, _ := cmd.Flags().GetString("type")
	target, _ := cmd.Flags().GetString("target")

	if eventType == "" {
		utils.CliErrorWithExit("--type is required.")
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	watcher := eventapi.NewWatcher(alpaconClient, eventapi.EventType(eventType), target)
	watcher.Start()
	defer watcher.Stop()

	if !watcher.WaitConnected(watchConnectTimeout) {
		// A rejected subscription is not retried, so its message is the useful one.
		// Stripped because the server wrote it and it lands in a terminal.
		if subErr := watcher.Err(); subErr != nil {
			utils.CliErrorWithExit("%s.", strip(subErr.Error()))
		}
		utils.CliErrorWithExit("Timed out connecting to the event channel after %s.", watchConnectTimeout)
	}

	utils.CliInfo("Watching %s events. Press Ctrl+C to stop.", strip(eventType))

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	// Runs before watcher.Stop, so a second Ctrl+C still kills a hung teardown.
	defer signal.Stop(sigChan)

	for {
		select {
		case <-sigChan:
			return
		case <-watcher.Reconnected():
			utils.CliWarning("Reconnected to the event channel. Events published while disconnected were not delivered.")
		case failErr := <-watcher.ReconnectFailed():
			utils.CliWarning("Lost the event channel and still reconnecting: %s. No events are being delivered until it recovers.", strip(failErr.Error()))
		case frame := <-watcher.Frames():
			if err := renderEvent(os.Stdout, frame, utils.OutputFormat, target, time.Now()); err != nil {
				utils.CliWarning("%s.", err)
			}
		}
	}
}
