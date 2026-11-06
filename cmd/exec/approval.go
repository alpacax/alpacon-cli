package exec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// approvalWaitMessage labels the --wait spinner. Named because each stretch of
// waiting gets a spinner of its own (see RunExecWithApprovalWait), and two
// labels drifting apart would read as two different waits.
const approvalWaitMessage = "Waiting for approval in the Alpacon console..."

const (
	// outcomePending covers every status that is not a decision, the absent
	// field included: a value nobody can read as an answer must not end a wait.
	outcomePending approvalOutcome = iota
	outcomeApproved
	// outcomeRejected and outcomeExpired both settle without a grant and both exit
	// ExitCodeNotApproved. They stay apart because the sentence the user reads
	// differs: one sends them back to the reviewer, the other to a fresh request.
	outcomeRejected
	outcomeExpired
)

// approvalWaitPollInterval is the base gap of the --wait poll loop—slower than
// the MFA poll (api/mfa/mfa.go) since each tick reads the command detail rather
// than re-running the command; a var so tests can shorten it.
var approvalWaitPollInterval = 5 * time.Second

// approvalOutcome is what the wait loop reads off a command's sudo grant
// status: keep waiting, take the grant, or stop because none is coming.
type approvalOutcome int

// approvalOutcomeOf reads a command detail's sudo grant status into a decision
// the wait loop can switch on. authorized is the only grant; rejected and
// expired are both settled without one. Everything else—an in-progress status,
// an empty string, or the field's absence on an older server—keeps waiting.
func approvalOutcomeOf(status *string) approvalOutcome {
	if status == nil {
		return outcomePending
	}
	switch *status {
	case "authorized":
		return outcomeApproved
	case "rejected":
		return outcomeRejected
	case "expired":
		return outcomeExpired
	default:
		return outcomePending
	}
}

// denialCodePresent reports whether output contains the plugin's terminal denial
// line for the given code. It anchors on the full "Alpacon denied this sudo
// command (CODE)." line—including the trailing period the plugin emits—never a
// bare "(CODE)" token, so a command whose own output prints the token cannot
// forge a match on a command that succeeded. Every code-specific detector routes
// through here, and firstDenialCode scans for the same sudoDenialLinePrefix, so

// approvalDenialOutput returns the captured output of a denial that left an
// approval request in flight: the command exited non-zero (a real denial always
// surfaces as a RemoteCommandError) AND the plugin's exact denial line for a
// pendingApproval code is in that output. Requiring both keeps a command that
// merely prints the token but succeeds (err == nil) from being mistaken for a
// pending approval. It hands back the output, not a bool, so a caller needing
// the denial code does not restate the rule for what counts as one.
func approvalDenialOutput(err error) (string, bool) {
	var remoteErr *event.RemoteCommandError
	if !errors.As(err, &remoteErr) || !hasSudoApprovalDenial(remoteErr.Output) {
		return "", false
	}
	return remoteErr.Output, true
}

// isApprovalDenial reports whether err is a denial with an approval request in flight.
func isApprovalDenial(err error) bool {
	_, ok := approvalDenialOutput(err)
	return ok
}

// pollApprovalOnce reads the grant the denial left behind. The command it names
// already ran and was blocked, so this is a status read, not a re-attempt.
func pollApprovalOnce(ac *client.AlpaconClient, cmdID string) (approvalOutcome, string, error) {
	details, err := getCommandByID(ac, cmdID)
	if err != nil {
		return outcomePending, "", err
	}
	requestID := ""
	if details.SudoApprovalRequestID != nil {
		requestID = *details.SudoApprovalRequestID
	}
	return approvalOutcomeOf(details.SudoGrantStatus), requestID, nil
}

// RunExecWithApprovalWait runs a command via RunExecWithPresenceStepUp and, when
// it is denied with an approval request in flight (a pendingApproval code) and
// waitTimeout is positive, blocks and polls the command's detail until a
// reviewer grants or refuses the request out of band, or the bounded timeout
// elapses. When waitTimeout is zero or negative, or the denial carries a
// terminal code, it returns the first err unchanged so the caller's
// pending/denial handling runs.
//
// A tick that never reached the server is not an answer: the loop backs off, and
// a timeout—or a run of failed polls long enough to give up on—reports the denial
// that opened the wait, so the caller still exits on the pending contract rather
// than as a generic failure.
//
// The poll reads the command's own detail (sudo_grant_status) rather than
// re-submitting it: the denial carries the command id, and re-attempting a
// command still pending approval would file a fresh approval request on every
// tick. The poll mirrors the MFA step-up structure (api/mfa/mfa.go): a spinner,
// a timer, and a precise deadline.
func RunExecWithApprovalWait(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID, purpose string, waitTimeout time.Duration, out io.Writer) error {
	return runWithApprovalWait(ac, func() error {
		return runPresenceStepUp(ac, serverName, command, username, groupname, env, workSessionID, purpose, out)
	}, waitTimeout, out)
}

// RunFileExecWithApprovalWait is RunExecWithApprovalWait for the verified file
// lane. Only the submission differs—the file object in place of a command
// line—so the presence step-up, the approval wait and the re-run after a grant
// are the same layers the generic lane runs through.
func RunFileExecWithApprovalWait(ac *client.AlpaconClient, serverName string, file event.FileExecution, username, groupname, workSessionID, purpose string, waitTimeout time.Duration, out io.Writer) error {
	return runWithApprovalWait(ac, func() error {
		return runWithPresenceStepUp(ac, serverName, func() error {
			// One stream for the attempt and its MFA retries; see RunCommandWithRetry.
			stream := event.NewCommandStream(ac)
			defer stream.Close()
			return runWithRetry(ac, serverName, func() error {
				return stream.RunFileCommand(serverName, file, username, groupname, workSessionID, purpose, out)
			})
		})
	}, waitTimeout, out)
}

// runWithApprovalWait is RunExecWithApprovalWait over a commandRun: run is the
// first attempt and, once a reviewer grants the request, the re-run.
func runWithApprovalWait(ac *client.AlpaconClient, run commandRun, waitTimeout time.Duration, out io.Writer) error {
	err := run()

	// Status-hold: the server parked this job at awaiting_approval (it never ran).
	// With --wait, resubscribe to the same job and stream once approved instead of
	// re-submitting; without --wait, surface it for HandlePendingApproval.
	var pendingErr *event.PendingApprovalError
	if errors.As(err, &pendingErr) {
		if waitTimeout <= 0 {
			return err
		}
		spinner := utils.NewSpinner("Waiting for approval in the Alpacon console (output streams once approved)...")
		spinner.Start()
		defer spinner.Stop()
		// The wait is inside the stream call, so the spinner has to survive it—
		// StopWriter retires it the moment the approved command's output starts.
		return streamApprovedCommand(ac, pendingErr.CommandID, spinner.StopWriter(out), waitTimeout)
	}

	if waitTimeout <= 0 || !isApprovalDenial(err) {
		return err
	}

	var denialErr *event.RemoteCommandError
	if !errors.As(err, &denialErr) || denialErr.CommandID == "" {
		// Nothing to poll: fall back to reporting the denial as it stands.
		return err
	}
	cmdID := denialErr.CommandID

	spinner := utils.NewSpinner(approvalWaitMessage)
	spinner.Start()
	// Every return path below stops the spinner before it writes, which is
	// load-bearing for output ordering; this backstops paths added later. Stop
	// on a stopped spinner is a no-op.
	defer spinner.Stop()

	pendingDenial := err
	started := time.Now()
	deadline := started.Add(waitTimeout)
	timer := time.NewTimer(waitTimeout)
	defer timer.Stop()
	poll := time.NewTimer(approvalWaitPollInterval)
	defer poll.Stop()
	failures := 0
	throttles := 0
	lastRequestID := ""
	budget := utils.NewThrottleBudget(waitTimeout)
	// lastRefusal is the refusal (a 429 in practice) the latest status read got,
	// if it got one, so a wait that times out on it still prints its hint.
	var lastRefusal error
	for {
		select {
		case <-timer.C:
			spinner.Stop()
			// Report only the timeout; the caller's pending-approval message already names --wait-approval.
			utils.CliWarning("%s", utils.HintOnly(fmt.Sprintf("Approval wait timed out after %s; the command is still pending.", waitTimeout), lastRefusal))
			return pendingWithRequestID(pendingDenial, lastRequestID)
		case <-poll.C:
			outcome, requestID, err := pollApprovalOnce(ac, cmdID)
			lastRefusal = nil
			if err != nil && utils.IsRefusalStatus(utils.HTTPStatusCode(err)) {
				lastRefusal = err
			}
			switch {
			case err != nil && isPollFailure(err):
				// A 429 says the server is answering, so it is not counted toward
				// the cap below—the throttle budget and the deadline bound it
				// instead, and counting it would end the wait before the budget it
				// just spent could carry it anywhere.
				if utils.HTTPStatusCode(err) == http.StatusTooManyRequests {
					delay := utils.NextPollBackoff(approvalWaitPollInterval, throttles, utils.RetryAfter(err))
					throttles++
					budget.WarnThrottled(delay)
					if newDeadline, extended := budget.Extend(deadline, delay); extended {
						deadline = newDeadline
						// No drain before Reset: under this module's Go 1.23+ timer
						// semantics the channel is unbuffered and a receive after Stop
						// is guaranteed to block, so the drain could only hang.
						timer.Stop()
						timer.Reset(time.Until(deadline))
					}
					poll.Reset(delay)
					continue
				}
				failures++
				if failures >= utils.MaxConsecutivePollFailures {
					spinner.Stop()
					// The approval request is still open, so this exits on the
					// pending contract like the timeout does. Exit 1 reads as
					// retryable, and an agent answers it by re-running exec and
					// filing a second request for the same command.
					utils.CliWarning("Approval wait gave up after %d failed polls (%s); the command is still pending.", failures, err)
					return pendingWithRequestID(pendingDenial, lastRequestID)
				}
				poll.Reset(utils.NextPollBackoff(approvalWaitPollInterval, failures-1, utils.RetryAfter(err)))
				continue
			case err != nil:
				spinner.Stop()
				// A fatal 4xx on the status read says nothing about the approval
				// request, which is still open—so this exits on the pending contract
				// like the give-up branch above, rather than on an exit 1 an agent
				// answers by filing a second request for the same command.
				utils.CliWarning("Approval status read failed (%s); the command is still pending.", err)
				return pendingWithRequestID(pendingDenial, lastRequestID)
			}
			failures = 0
			throttles = 0
			// The budget is not reset here: reading a grant that is still pending is
			// not progress, and this loop returns the moment the status becomes one.
			// So the allowance runs for the life of the wait, like the deadline it
			// extends—resetting per poll would re-earn it on any non-429 response.
			if requestID != "" {
				lastRequestID = requestID
			}
			switch outcome {
			case outcomeApproved:
				spinner.Stop()
				return runAfterApproval(run)
			case outcomeRejected, outcomeExpired:
				spinner.Stop()
				// Settled without a grant. CommandRejectedError is what already carries
				// this to ExitCodeNotApproved, so the two denial shapes exit alike; only
				// the sentence they print differs.
				return &event.CommandRejectedError{CommandID: cmdID, Expired: outcome == outcomeExpired}
			}
			// A fixed gap over a 30m wait outspends the server's service-token
			// throttle, so the gap widens as the wait ages.
			poll.Reset(utils.NextPollTick(approvalWaitPollInterval, time.Since(started)))
		}
	}
}

// runAfterApproval runs the command once the grant is authorized. A denial here
// means the grant went somewhere else—it expired, or another attempt spent
// it—so this reports that instead of opening a second wait the user never
// asked for.
func runAfterApproval(run commandRun) error {
	err := run()
	// Either shape means the grant did not carry this run, and each gets the
	// sentence its own way out needs. A repeated denial is answered by filing a
	// fresh request; a job the server parked runs on its own once approved, so
	// telling that caller to re-run would contradict the pending text
	// HandlePendingApproval prints moments later on the same stream.
	var pendingErr *event.PendingApprovalError
	switch {
	case isApprovalDenial(err):
		utils.CliWarning("The approval was granted but the command was denied again; the grant appears already used or expired. Re-run the command to request approval again.")
	case errors.As(err, &pendingErr):
		utils.CliWarning("The approval was granted but the grant did not carry this run; the server is holding the command for a fresh approval.")
	}
	return err
}

// pendingWithRequestID copies the denial so the wait can attach what it learned
// while polling. The original is the caller's; mutating it would surprise them.
func pendingWithRequestID(denial error, requestID string) error {
	var remoteErr *event.RemoteCommandError
	if requestID == "" || !errors.As(denial, &remoteErr) {
		return denial
	}
	withID := *remoteErr
	withID.ApprovalRequestID = requestID
	return &withID
}

// isPollFailure separates a status read that carried no usable answer from one
// that did: a network error, an undecodable body, or a retryable HTTP status
// (any but a fatal 4xx, so 429/5xx keep the wait alive). It costs nothing to be
// liberal here—the poll only reads the command detail, so a false retry wastes a
// tick, never a re-run of the command. A run of them is capped at
// MaxConsecutivePollFailures, except for 429s: the throttle budget bounds those.
//
// *url.Error satisfies net.Error, so one errors.As covers both the dial and the
// round-trip failure—neither reached the server. *json.SyntaxError and
// *json.UnmarshalTypeError cover a body that did reach it but would not decode
// (a proxy error page under a JSON content type, or a response shape that
// drifted).
func isPollFailure(err error) bool {
	if err == nil {
		return false
	}
	if status := utils.HTTPStatusCode(err); status != 0 {
		return !utils.IsFatalClientError(status)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}
