package ftp

import (
	"fmt"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// mfaGate lets the requests of transfers on one client and server share one MFA
// wait: the bulk status polls run concurrently and are refused together. The
// lock is held only for the wait itself. endedAt and endErr record when the
// last wait ended and how, so a request refused before that moment takes its
// outcome instead of starting another wait. Transfers of another client or
// server have their own gate.
type mfaGate struct {
	mu      sync.Mutex
	endedAt time.Time
	endErr  error
}

type mfaGateKey struct {
	ac       *client.AlpaconClient
	serverID string
}

var mfaGates sync.Map // mfaGateKey -> *mfaGate

func gateFor(ac *client.AlpaconClient, serverID string) *mfaGate {
	gate, _ := mfaGates.LoadOrStore(mfaGateKey{ac: ac, serverID: serverID}, &mfaGate{})
	return gate.(*mfaGate)
}

// await runs wait unless one ended after refusedAt, in which case it returns
// that wait's outcome. waited reports whether this call ran the wait.
func (g *mfaGate) await(refusedAt time.Time, wait func() error) (waited bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.endedAt.After(refusedAt) {
		return false, g.endErr
	}
	err = wait()
	g.endedAt, g.endErr = time.Now(), err
	return true, err
}

// transferStep sends one request of a transfer the server already created, as
// its own retry unit. If MFA freshness lapsed and the server refuses it for
// MFA, the user is prompted and only that request is sent again once MFA
// completes; the transfer is never created a second time. Any other result is
// returned as is, untagged: a request after the create is never a submission
// that may be resent. Refusals that arrive together share one MFA wait, and
// when it fails each of them returns its error without prompting again.
func transferStep(ac *client.AlpaconClient, serverID string, step func() error) error {
	return transferStepProbed(ac, serverID, time.Time{}, step, nil)
}

// transferStepProbed is transferStep for a step that is a whole loop of
// requests, a status poll: the MFA wait retries probe, one request, and the
// step runs again in full only after the wait, outside the gate, so the
// refused steps of sibling transfers proceed in parallel. A nil probe is the
// step itself.
//
// A refusal that began before the last wait ended takes that wait's outcome. For
// a step of its own that moment is when the step started (a zero refusedAt). The
// polls of one bulk command are queued behind a concurrency limit, so some start
// only after a wait ended; the command passes the time it began so all of them
// count as refused before it. A refusal from a separate, later command starts
// after and still prompts.
func transferStepProbed(ac *client.AlpaconClient, serverID string, refusedAt time.Time, step, probe func() error) error {
	if refusedAt.IsZero() {
		refusedAt = time.Now()
	}
	retry := probe
	if retry == nil {
		retry = step
	}
	waits := 0
	for {
		err := step()
		if utils.StructuredErrorCode(err) != utils.AuthMFARequired {
			return err
		}
		// A step still refused after this many waits of its own is not a lapse
		// but a refusal MFA does not clear; end it instead of issuing links
		// without limit.
		if waits >= utils.MaxConsecutivePollFailures {
			return err
		}

		waited, werr := gateFor(ac, serverID).await(refusedAt, func() error {
			return utils.HandleCommonErrors(utils.MarkSubmission(err), "", utils.ErrorHandlerCallbacks{
				OnMFARequired: func(string) error { return mfa.HandleMFAErrorForServerID(ac, serverID) },
				RetryOperation: func() error {
					return utils.MarkSubmission(retry())
				},
			})
		})
		if werr != nil {
			return werr
		}
		if waited {
			waits++
		}
		if waited && probe == nil {
			// The retried request was the step itself, and it went through.
			return nil
		}
		// The wait that ran only proved MFA through the probe, or another
		// request's successful wait covered this refusal. Run the step again
		// through the same refusal handling: if MFA lapsed since, the refusal
		// starts after that wait and gets a wait of its own. Each further wait
		// needs the user to complete MFA, and a failed one returns above.
		refusedAt = time.Now()
	}
}

// pollTransfer is pollTransferStatus with the poll as a retry unit. refusedAt is
// the time the command began, for polls queued behind a concurrency limit; the
// zero time means the poll's own start (see transferStepProbed). A poll
// refused for MFA cannot say how the transfer ended, so when it is refused and
// the wait does not end in an answer, the error says the transfer may already
// have completed.
func pollTransfer(ac *client.AlpaconClient, serverID string, kind transferKind, id api.PathID, timeout time.Duration, refusedAt time.Time) (success bool, message string, err error) {
	refused := false
	note := func(stepErr error) error {
		if utils.StructuredErrorCode(stepErr) == utils.AuthMFARequired {
			refused = true
		}
		return stepErr
	}
	err = transferStepProbed(ac, serverID, refusedAt,
		func() error {
			var stepErr error
			success, message, stepErr = pollTransferStatus(ac, kind, id, timeout, pollTick)
			return note(stepErr)
		},
		func() error {
			// One status read: it tells whether MFA went through without
			// running the whole poll under the gate. A transient failure is
			// ridden through as the full poll rides through it, so a flaky read
			// does not end the wait.
			statusURL := transferStatusURL(kind, id)
			var probeErr error
			for failures := range utils.MaxConsecutivePollFailures {
				_, probeErr = ac.SendGetRequest(statusURL)
				if probeErr == nil || !utils.IsTransientRequestError(probeErr) {
					break
				}
				time.Sleep(utils.NextPollBackoff(pollTick, failures, utils.RetryAfter(probeErr)))
			}
			return note(probeErr)
		})
	if err != nil && refused {
		err = fmt.Errorf("%w (the %s may already have completed on the server; check before running it again)", err, kind.name)
	}
	return success, message, err
}
