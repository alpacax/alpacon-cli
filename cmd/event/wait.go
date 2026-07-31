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

const defaultWaitTimeout = 5 * time.Minute

var waitCmd = &cobra.Command{
	Use:   "wait --type TYPE [--target TARGET_ID]",
	Short: "Wait for one event, then exit",
	Long: `Block until one event decides the wait, print it, and exit.

The outcome is carried by the exit code, so a script never has to parse prose:

  0  the event arrived and counts as success (a work session was approved)
  4  the wait timed out or was interrupted—the outcome is still open
  6  the event arrived and counts as failure (rejected, expired, revoked,
     cancelled, or completed)

One line is written to stdout, in the same shape 'alpacon event watch' uses:
with --output json the server frame compacted to a single line, otherwise four
fixed fields. Everything else goes to stderr.

For work_session the end condition is built in. For any other type, name the
sub types that end the wait with --until.

After subscribing, the current state is read once over REST so an outcome that
landed between the subscribe and the first publish is not missed. That check is
repeated after every reconnect, since events published while disconnected are
lost—the event channel has no history to replay.

Run 'alpacon work-session ls' or the Alpacon console to find a target ID.`,
	Example: `  alpacon event wait --type work_session --target a1b2c3d4-5678-abcd-ef01-234567890abc
  alpacon event wait --type work_session --target a1b2c3d4-5678-abcd-ef01-234567890abc --timeout 30m
  alpacon event wait --type notification --until created`,
	Args: cobra.NoArgs,
	Run:  runWait,
}

func init() {
	waitCmd.Flags().String("type", "", "Event type to subscribe to, e.g. work_session (required)")
	waitCmd.Flags().String("target", "", "Target resource ID; omit where the server allows a subscription without one")
	waitCmd.Flags().StringSlice("until", nil, "Sub types that end the wait (comma-separated). Overrides the built-in condition, and is required for a type this CLI has none for")
	waitCmd.Flags().Duration("timeout", defaultWaitTimeout, "How long to wait before giving up")

	EventCmd.AddCommand(waitCmd)
}

func runWait(cmd *cobra.Command, _ []string) {
	eventType, _ := cmd.Flags().GetString("type")
	target, _ := cmd.Flags().GetString("target")
	until, _ := cmd.Flags().GetStringSlice("until")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	if eventType == "" {
		utils.CliErrorWithExit("--type is required.")
	}
	if timeout <= 0 {
		utils.CliErrorWithExit("--timeout must be positive.")
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	opts, err := resolveWaitOptions(alpaconClient, eventapi.EventType(eventType), target, until, timeout)
	if err != nil {
		utils.CliErrorWithExit("%s.", err)
	}

	waiter := eventapi.NewWaiter(alpaconClient, eventapi.EventType(eventType), target, opts)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	// Runs before the waiter is torn down, so a second Ctrl+C still kills a hung teardown.
	defer signal.Stop(sigChan)
	go func() {
		<-sigChan
		waiter.Stop()
	}()

	go reportOutages(waiter)

	utils.CliInfo("Waiting for a %s event (timeout %s). Press Ctrl+C to stop.", eventType, timeout)

	frame, outcome, err := waiter.Wait()
	if err != nil {
		// Stripped because the server wrote it and it lands in a terminal.
		utils.CliErrorWithExit("%s.", strip(err.Error()))
	}

	switch outcome {
	case eventapi.OutcomeTimeout:
		utils.CliErrorWithExitCode(utils.ExitCodePendingApproval,
			"Timed out after %s with no matching %s event. The outcome is still open.", timeout, eventType)
	case eventapi.OutcomeCanceled:
		utils.CliErrorWithExitCode(utils.ExitCodePendingApproval,
			"Interrupted before a matching %s event arrived. The outcome is still open.", eventType)
	}

	if err := renderEvent(os.Stdout, frame, utils.OutputFormat, target, time.Now()); err != nil {
		utils.CliErrorWithExit("%s.", err)
	}

	if outcome == eventapi.OutcomeFailed {
		os.Exit(utils.ExitCodeNotApproved)
	}
}

// reportOutages runs for the life of the command. Both conditions can recur: a catch-up
// can fail on the first check and again after any reconnect, and reconnects can keep
// failing. Draining ReconnectFailed also keeps the watcher's one-slot notice free so a
// later outage is not dropped.
func reportOutages(waiter *eventapi.Waiter) {
	for {
		select {
		case err := <-waiter.CatchUpFailed():
			utils.CliWarning("Could not read the current state: %s. Still waiting for an event.", strip(err.Error()))
		case err := <-waiter.ReconnectFailed():
			utils.CliWarning("Lost the event channel and still reconnecting: %s. No events are being delivered until it recovers.", strip(err.Error()))
		}
	}
}
