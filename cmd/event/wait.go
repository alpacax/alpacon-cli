package event

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	eventapi "github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var waitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Wait for one event, then exit",
	Long: `Block until one event decides the wait, print it, and exit.

The outcome is carried by the exit code, so a script never has to parse prose:

  0  an event in the success set arrived
  1  the wait could not run: connection failed or the subscription was
     rejected
  2  a flag or argument was rejected
  4  timed out or interrupted—the outcome is still open
  6  an event in the failure set arrived. Only a type with a built-in end
     condition has a failure set (work_session's is rejected, expired,
     revoked, cancelled, or completed), so this cannot happen when --until
     names the end condition instead

One line is written to stdout, in the same shape 'alpacon event watch' uses:
with --output json the server frame compacted to a single line, otherwise four
fixed fields. Everything else goes to stderr.

For work_session the end condition is built in. For any other type, name the
sub types that end the wait with --until.

For a type with a built-in end condition, passing --target also reads the
current state once over REST after subscribing, so an outcome that landed
between the subscribe and the first publish is not missed; the read is repeated
after every reconnect, since events published while disconnected are lost—the
event channel has no history to replay. No other type gets that read.

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
	waitCmd.Flags().Duration("timeout", utils.DefaultApprovalWaitTimeout, "How long to wait before giving up")

	EventCmd.AddCommand(waitCmd)
}

func validateEventTarget(target string) (string, error) {
	if target == "" {
		return "", nil
	}
	return api.RequireID(target, "target")
}

func runWait(cmd *cobra.Command, _ []string) {
	eventType, _ := cmd.Flags().GetString("type")
	target, _ := cmd.Flags().GetString("target")
	untilRaw, _ := cmd.Flags().GetStringSlice("until")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	target, err := validateEventTarget(target)
	if err != nil {
		utils.CliUsageErrorEnvelopeWithExit(opWait, "%s.", strip(err.Error()))
	}

	// The slice reaches here as typed: '--until "approved, activated"' leaves a leading
	// space on the second entry, which would never match a sub type.
	until := utils.CompactStrings(untilRaw)

	if eventType == "" {
		utils.CliUsageErrorEnvelopeWithExit(opWait, "--type is required.")
	}
	// Rejected rather than ignored: falling back to the built-in condition would run a
	// wait the caller did not ask for.
	if cmd.Flags().Changed("until") && len(until) == 0 {
		utils.CliUsageErrorEnvelopeWithExit(opWait, "--until needs at least one sub type.")
	}
	if timeout <= 0 {
		utils.CliUsageErrorEnvelopeWithExit(opWait, "--timeout must be positive.")
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorEnvelopeWithExit(opWait, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	opts, err := resolveWaitOptions(alpaconClient, eventapi.EventType(eventType), target, until, timeout)
	if err != nil {
		utils.CliUsageErrorEnvelopeWithExit(opWait, "%s.", strip(err.Error()))
	}

	waiter := eventapi.NewWaiter(alpaconClient, eventapi.EventType(eventType), target, opts)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	// Covers the plain-return path (no signal ever arrived); harmless and idempotent
	// alongside the Stop call below.
	defer signal.Stop(sigChan)
	go func() {
		<-sigChan
		// Disarmed here, not only in the deferred call above: waiter.Stop can hang closing
		// the WebSocket, and every exit path below it uses os.Exit, which skips defers—so
		// this goroutine is the only place a second Ctrl+C is guaranteed to restore the
		// default terminate disposition before the (possibly stuck) Stop call.
		signal.Stop(sigChan)
		waiter.Stop()
	}()

	stopOutages := startOutageReporter(waiter.CatchUpFailed(), waiter.ReconnectFailed(), warnOutage)

	// Stripped for the same reason server text is: --type lands in a terminal, and a CI
	// script or an agent may build it from a source the caller does not control.
	displayType := strip(eventType)

	utils.CliInfo("Waiting for a %s event (timeout %s). Press Ctrl+C to stop.", displayType, timeout)

	frame, outcome, err := waiter.Wait()
	stopOutages()
	if err != nil {
		// Stripped because the server wrote it and it lands in a terminal.
		utils.CliErrorEnvelopeWithExit(opWait, err, "%s.", strip(err.Error()))
	}

	// PrintPendingApproval, not CliError: exit 4 owes a machine consumer the
	// pending_approval envelope under --output json, the same as every other surface
	// that returns it. No retry action: unlike exec's and create's, the only retry here
	// is the invocation the consumer just ran, so naming it back carries nothing.
	switch outcome {
	case eventapi.OutcomeTimeout:
		utils.PrintPendingApproval(
			fmt.Sprintf("Timed out after %s with no matching %s event. The outcome is still open.", timeout, displayType),
			"", utils.NextAction{})
		os.Exit(utils.ExitCodePendingApproval)
	case eventapi.OutcomeCanceled:
		utils.PrintPendingApproval(
			fmt.Sprintf("Interrupted before a matching %s event arrived. The outcome is still open.", displayType),
			"", utils.NextAction{})
		os.Exit(utils.ExitCodePendingApproval)
	}

	// A render failure must not change a settled outcome's exit code—renderEvent writes
	// nothing to stdout on failure, so the failure itself only ever reaches stderr.
	if renderErr := renderEvent(os.Stdout, frame, utils.OutputFormat, target, time.Now()); renderErr != nil {
		utils.CliWarning("%s.", renderErr)
	}

	if outcome == eventapi.OutcomeFailed {
		exitNotApproved(displayType)
	}
}

// exitNotApproved: the failure event is already on stdout, so under json only the envelope marks it.
func exitNotApproved(displayType string) {
	utils.ExitWithFailureJSON(utils.Failure{
		ExitCode:  utils.ExitCodeNotApproved,
		ErrorCode: utils.ErrorCodeNotApproved,
		Operation: opWait,
		Message:   fmt.Sprintf("A %s event in the failure set arrived.", displayType),
	})
	os.Exit(utils.ExitCodeNotApproved)
}

// startOutageReporter drains both outage channels (each can recur) until stop; stop returns
// only after the goroutine exits, so nothing prints after an envelope.
func startOutageReporter(catchUp, reconnect <-chan error, warn func(cause error, reconnect bool)) (stop func()) {
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-quit:
				return
			case err := <-catchUp:
				warn(err, false)
			case err := <-reconnect:
				warn(err, true)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
}

func warnOutage(cause error, reconnect bool) {
	if reconnect {
		utils.CliWarning("Lost the event channel and still reconnecting: %s. No events are being delivered until it recovers.", strip(cause.Error()))
		return
	}
	utils.CliWarning("Could not read the current state: %s. Still waiting for an event.", strip(cause.Error()))
}
