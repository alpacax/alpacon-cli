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

var waitCmd = newWaitCommand(client.NewAlpaconAPIClient)

func newWaitCommand(newClient func() (*client.AlpaconClient, error)) *cobra.Command {
	cmd := &cobra.Command{
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
		Example: `  alpacon event wait --type work_session --target 550e8400-e29b-41d4-a716-446655440000
  alpacon event wait --type work_session --target 550e8400-e29b-41d4-a716-446655440000 --timeout 30m
  alpacon event wait --type notification --until created`,
		Args: cobra.NoArgs,
		RunE: utils.WithExitError(func(cmd *cobra.Command, _ []string) error { return runWait(cmd, newClient) }),
	}
	cmd.Flags().String("type", "", "Event type to subscribe to, e.g. work_session (required)")
	cmd.Flags().String("target", "", "Target resource ID; omit where the server allows a subscription without one")
	cmd.Flags().StringSlice("until", nil, "Sub types that end the wait (comma-separated). Overrides the built-in condition, and is required for a type this CLI has none for")
	cmd.Flags().Duration("timeout", utils.DefaultApprovalWaitTimeout, "How long to wait before giving up")
	return cmd
}

func init() {
	EventCmd.AddCommand(waitCmd)
}

func validateEventTarget(target string) (string, error) {
	if target == "" {
		return "", nil
	}
	pathID, err := api.RequireID(target, "target")
	if err != nil {
		return "", err
	}
	return pathID.String(), nil
}

func runWait(cmd *cobra.Command, newClient func() (*client.AlpaconClient, error)) error {
	eventType, _ := cmd.Flags().GetString("type")
	target, _ := cmd.Flags().GetString("target")
	untilRaw, _ := cmd.Flags().GetStringSlice("until")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	target, err := validateEventTarget(target)
	if err != nil {
		return utils.NewUsageEnvelopeExitError(opWait, "%s.", strip(err.Error()))
	}

	// The slice reaches here as typed: '--until "approved, activated"' leaves a leading
	// space on the second entry, which would never match a sub type.
	until := utils.CompactStrings(untilRaw)

	if eventType == "" {
		return utils.NewUsageEnvelopeExitError(opWait, "--type is required.")
	}
	// Rejected rather than ignored: falling back to the built-in condition would run a
	// wait the caller did not ask for.
	if cmd.Flags().Changed("until") && len(until) == 0 {
		return utils.NewUsageEnvelopeExitError(opWait, "--until needs at least one sub type.")
	}
	if timeout <= 0 {
		return utils.NewUsageEnvelopeExitError(opWait, "--timeout must be positive.")
	}

	alpaconClient, err := newClient()
	if err != nil {
		return utils.NewEnvelopeExitError(opWait, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	opts, err := resolveWaitOptions(alpaconClient, eventapi.EventType(eventType), target, until, timeout)
	if err != nil {
		return utils.NewUsageEnvelopeExitError(opWait, "%s.", strip(err.Error()))
	}

	waiter := eventapi.NewWaiter(alpaconClient, eventapi.EventType(eventType), target, opts)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)
	// Closed on return, so neither goroutine outlives runWait; the reporter is also joined, so
	// no warning it was already printing lands after the failure line.
	var reporter sync.WaitGroup
	defer reporter.Wait()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sigChan:
		case <-done:
			return
		}
		// Disarmed here too: waiter.Stop can hang closing the WebSocket, and the deferred
		// Stop runs only once Wait returns, so only this goroutine lets a second Ctrl+C terminate.
		signal.Stop(sigChan)
		waiter.Stop()
	}()

	reporter.Go(func() { reportOutages(waiter, done) })

	// Stripped for the same reason server text is: --type lands in a terminal, and a CI
	// script or an agent may build it from a source the caller does not control.
	displayType := strip(eventType)

	utils.CliInfo("Waiting for a %s event (timeout %s). Press Ctrl+C to stop.", displayType, timeout)

	frame, outcome, err := waiter.Wait()
	if err != nil {
		// Stripped because the server wrote it and it lands in a terminal.
		return utils.NewEnvelopeExitError(opWait, err, "%s.", strip(err.Error()))
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
		return utils.NewSilentExitError(utils.ExitCodePendingApproval)
	case eventapi.OutcomeCanceled:
		utils.PrintPendingApproval(
			fmt.Sprintf("Interrupted before a matching %s event arrived. The outcome is still open.", displayType),
			"", utils.NextAction{})
		return utils.NewSilentExitError(utils.ExitCodePendingApproval)
	}

	// A render failure must not change a settled outcome's exit code—renderEvent writes
	// nothing to stdout on failure, so the failure itself only ever reaches stderr.
	if renderErr := renderEvent(cmd.OutOrStdout(), frame, utils.OutputFormat, target, time.Now()); renderErr != nil {
		utils.CliWarning("%s.", renderErr)
	}

	if outcome == eventapi.OutcomeFailed {
		return utils.NewSilentExitError(utils.ExitCodeNotApproved)
	}
	return nil
}

// reportOutages runs until done closes, when runWait returns. Both conditions can recur: a catch-up
// can fail on the first check and again after any reconnect, and reconnects can keep
// failing. Draining ReconnectFailed also keeps the watcher's one-slot notice free so a
// later outage is not dropped.
func reportOutages(waiter *eventapi.Waiter, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case err := <-waiter.CatchUpFailed():
			// Reworded rather than flattened, so a refusal's hint still prints.
			utils.CliWarning("Could not read the current state: %s. Still waiting for an event.", utils.Reword(err, strip(err.Error())))
		case err := <-waiter.ReconnectFailed():
			utils.CliWarning("Lost the event channel and still reconnecting: %s. No events are being delivered until it recovers.", utils.Reword(err, strip(err.Error())))
		}
	}
}
