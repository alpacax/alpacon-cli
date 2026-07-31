package event

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/alpacax/alpacon-cli/client"
)

const (
	// waitConnectTimeout covers one provision + dial (watchHandshakeTimeout) plus the
	// subscribe round trip that follows it.
	waitConnectTimeout = 15 * time.Second

	// catchUpSource marks a frame the CLI built from a REST read. A server frame never
	// carries it, so a consumer can tell the two apart.
	catchUpSource = "catch_up"
)

const (
	OutcomeError Outcome = iota
	OutcomeMatched
	OutcomeFailed
	OutcomeTimeout
	OutcomeCanceled
)

// Outcome is how a Wait ended. OutcomeError is the zero value: it is only returned
// alongside a non-nil error, so a caller that forgets to check err still sees it.
type Outcome int

// CatchUpFunc reads the target's current state over REST and returns it in the event
// channel's sub_type vocabulary. It closes the window between subscribing and the
// server's next publish, which is otherwise unrecoverable: the channel has no history.
type CatchUpFunc func() (subType string, err error)

// WaitOptions carries everything type-specific about a wait. Keeping it injected is why
// this package needs no knowledge of any event type.
type WaitOptions struct {
	// CatchUp may be nil for a type with no REST projection.
	CatchUp CatchUpFunc
	OK      []string
	Fail    []string
	Timeout time.Duration
}

// Waiter blocks until one event decides the wait, then returns it. Reconnects and
// session reprovisioning are inherited from Watcher.
type Waiter struct {
	*Watcher
	opts          WaitOptions
	catchUpFailed chan error
}

// waitFrame reads only the field that decides a wait; the frame reaches the caller
// verbatim, so nothing else is modelled here.
type waitFrame struct {
	Payload struct {
		SubType string `json:"sub_type"`
	} `json:"payload"`
}

// synthesizedFrame is the shape a catch-up result is rendered as, so stdout carries one
// schema whether the outcome arrived over the channel or over REST.
type synthesizedFrame struct {
	EventType string                  `json:"event_type"`
	Payload   synthesizedFramePayload `json:"payload"`
}

type synthesizedFramePayload struct {
	SubType string `json:"sub_type"`
	Source  string `json:"source"`
}

// NewWaiter constructs a Waiter without connecting. Call Wait.
func NewWaiter(ac *client.AlpaconClient, eventType EventType, targetID string, opts WaitOptions) *Waiter {
	return &Waiter{
		Watcher:       NewWatcher(ac, eventType, targetID),
		opts:          opts,
		catchUpFailed: make(chan error, 1),
	}
}

// CatchUpFailed reports a catch-up that could not be read. Buffered at one: a caller
// that misses a notice loses nothing a later one does not also carry.
func (w *Waiter) CatchUpFailed() <-chan error { return w.catchUpFailed }

// Wait blocks until an event matches, the timeout elapses, or Stop is called. The
// returned frame is the server's bytes verbatim, except on a catch-up hit where it is
// synthesized. A non-nil error means the wait could not run; Outcome is meaningless then.
func (w *Waiter) Wait() ([]byte, Outcome, error) {
	w.Start()
	defer w.Stop()

	if !w.WaitConnected(waitConnectTimeout) {
		// A rejected subscription is not retried, so its message is the useful one.
		if err := w.Err(); err != nil {
			return nil, OutcomeError, err
		}
		if w.isStopped() {
			return nil, OutcomeCanceled, nil
		}
		return nil, OutcomeError, fmt.Errorf("timed out connecting to the event channel after %s", waitConnectTimeout)
	}

	// Only now: a catch-up before the subscription stands would leave a window in which
	// a publish reaches nobody.
	if frame, outcome, decided := w.runCatchUp(); decided {
		return frame, outcome, nil
	}

	timer := time.NewTimer(w.opts.Timeout)
	defer timer.Stop()

	for {
		select {
		case <-w.done:
			return nil, OutcomeCanceled, nil
		case <-timer.C:
			return nil, OutcomeTimeout, nil
		case <-w.Reconnected():
			if frame, outcome, decided := w.runCatchUp(); decided {
				return frame, outcome, nil
			}
		case raw := <-w.Frames():
			if outcome, decided := w.classify(subTypeOf(raw)); decided {
				return raw, outcome, nil
			}
		}
	}
}

func (w *Waiter) isStopped() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (w *Waiter) runCatchUp() ([]byte, Outcome, bool) {
	if w.opts.CatchUp == nil {
		return nil, OutcomeError, false
	}

	subType, err := w.opts.CatchUp()
	if err != nil {
		select {
		case w.catchUpFailed <- err:
		default:
		}
		return nil, OutcomeError, false
	}

	outcome, decided := w.classify(subType)
	if !decided {
		return nil, OutcomeError, false
	}
	return w.synthesize(subType), outcome, true
}

// classify reports the outcome for a sub_type and whether it decides the wait.
func (w *Waiter) classify(subType string) (Outcome, bool) {
	switch {
	case subType == "":
		return OutcomeError, false
	case slices.Contains(w.opts.OK, subType):
		return OutcomeMatched, true
	case slices.Contains(w.opts.Fail, subType):
		return OutcomeFailed, true
	default:
		return OutcomeError, false
	}
}

func (w *Waiter) synthesize(subType string) []byte {
	frame, err := json.Marshal(synthesizedFrame{
		EventType: string(w.eventType),
		Payload:   synthesizedFramePayload{SubType: subType, Source: catchUpSource},
	})
	if err != nil {
		return nil
	}
	return frame
}

// subTypeOf returns "" for a frame that cannot be read, which classify treats as
// undecided—a malformed frame must not end a wait.
func subTypeOf(raw []byte) string {
	var frame waitFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return ""
	}
	return frame.Payload.SubType
}
