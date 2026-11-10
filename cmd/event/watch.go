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

var watchCmd = newWatchCommand(client.NewAlpaconAPIClient)

func newWatchCommand(newClient func() (*client.AlpaconClient, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
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
		Example: `  alpacon event watch --type work_session --target 550e8400-e29b-41d4-a716-446655440000
  alpacon event watch --type work_session --target 550e8400-e29b-41d4-a716-446655440000 --output json
  alpacon event watch --type notification`,
		Args: cobra.NoArgs,
		RunE: utils.WithExitError(func(cmd *cobra.Command, _ []string) error { return runWatch(cmd, newClient) }),
	}
	cmd.Flags().String("type", "", "Event type to subscribe to, e.g. work_session or notification (required)")
	cmd.Flags().String("target", "", "Target resource ID; omit where the server allows a subscription without one")
	return cmd
}

func init() {
	EventCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, newClient func() (*client.AlpaconClient, error)) error {
	eventType, _ := cmd.Flags().GetString("type")
	target, _ := cmd.Flags().GetString("target")
	target, err := validateEventTarget(target)
	if err != nil {
		return utils.NewUsageEnvelopeExitError(opWatch, "%s.", strip(err.Error()))
	}

	if eventType == "" {
		return utils.NewUsageEnvelopeExitError(opWatch, "--type is required.")
	}

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewEnvelopeExitError(opWatch, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	watcher := eventapi.NewWatcher(alpaconClient, eventapi.EventType(eventType), target)
	watcher.Start()
	defer watcher.Close() // waits for the listener and the requests it makes

	if !watcher.WaitConnected(watchConnectTimeout) {
		// A rejected subscription is not retried, so its message is the useful one.
		// Stripped because the server wrote it and it lands in a terminal.
		if subErr := watcher.Err(); subErr != nil {
			return utils.NewEnvelopeExitError(opWatch, subErr, "%s.", strip(subErr.Error()))
		}
		return utils.NewEnvelopeExitError(opWatch, nil, "Timed out connecting to the event channel after %s.", watchConnectTimeout)
	}

	utils.CliInfo("Watching %s events. Press Ctrl+C to stop.", strip(eventType))

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	// Runs before watcher.Stop, so a second Ctrl+C still kills a hung teardown.
	defer signal.Stop(sigChan)

	for {
		select {
		case <-sigChan:
			return nil
		case <-watcher.Reconnected():
			utils.CliWarning("Reconnected to the event channel. Events published while disconnected were not delivered.")
		case failErr := <-watcher.ReconnectFailed():
			utils.CliWarning("Lost the event channel and still reconnecting: %s. No events are being delivered until it recovers.", utils.Reword(failErr, strip(failErr.Error())))
		case frame := <-watcher.Frames():
			if err := renderEvent(cmd.OutOrStdout(), frame, utils.OutputFormat, target, time.Now()); err != nil {
				utils.CliWarning("%s.", err)
			}
		}
	}
}
