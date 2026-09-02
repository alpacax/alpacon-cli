package utils

import "time"

const (
	// Poll pacing, in multiples of the caller's base tick, widening as the poll ages
	// and again once the server pushes back. A fixed 1s tick burns alpacon-server's
	// default 1000/hour service-token throttle in ~17 minutes, then starves itself—each
	// freed slot goes to a request that is throttled again.
	pollFastWindow    = 10
	pollMediumWindow  = 60
	pollMediumTick    = 5
	pollSlowTick      = 10
	pollBackoffFactor = 2

	// PollMaxBackoffTick is exported so a poll loop's own test (api/event,
	// cmd/exec, cmd/worksession) can state its capped wait as a constant
	// expression.
	PollMaxBackoffTick = 60

	// MaxConsecutivePollFailures bounds a wait's ride through failed polls: a run
	// of them means the CLI cannot reach the server, and saying so beats
	// reporting an outcome nobody read.
	MaxConsecutivePollFailures = 5

	// PollMaxThrottleExtensions bounds how many times a throttled poll may push its
	// deadline out. A duration budget alone is not enough: a server answering
	// Retry-After: 1 would buy a whole window one second at a time.
	PollMaxThrottleExtensions = 60
)

// ThrottleBudget is the allowance a poll spends to survive being rate limited.
// A 429 says the server is alive and pushing back, not that the work is lost, so
// the deadline moves rather than starving—but only this far.
type ThrottleBudget struct {
	limit      time.Duration
	spent      time.Duration
	extensions int
	warned     bool
}

// NewThrottleBudget returns a new budget that treats timeout as its cumulative
// extension limit.
func NewThrottleBudget(timeout time.Duration) *ThrottleBudget {
	return &ThrottleBudget{limit: timeout}
}

// Extend grows deadline by delay when the count budget allows it and the
// cumulative duration either still has room for delay or has not been spent at
// all yet, and reports whether it did.
//
// The very first extension in a window is granted whole even when delay alone
// overshoots limit: a zero-length grant would leave the poll no time to finish
// the wait a throttled server itself just handed it. Every extension after
// that must fit inside what remains, so one oversized grant cannot be
// followed by an unbounded run of further ones.
func (b *ThrottleBudget) Extend(deadline time.Time, delay time.Duration) (time.Time, bool) {
	if b.spent >= b.limit || b.extensions >= PollMaxThrottleExtensions {
		return deadline, false
	}
	b.spent += delay
	b.extensions++
	return deadline.Add(delay), true
}

// ShouldWarn reports true once, the first time it is called since construction
// or the last Reset—so a wait that stays throttled for a long time still warns
// only once.
func (b *ThrottleBudget) ShouldWarn() bool {
	if b.warned {
		return false
	}
	b.warned = true
	return true
}

// Reset clears the spent budget, extension count, and warning flag—called when
// the poll sees progress, so a throttle late in a long wait is not charged
// against an earlier, unrelated one.
func (b *ThrottleBudget) Reset() {
	b.spent = 0
	b.extensions = 0
	b.warned = false
}

// NextPollTick widens the gap as the wait ages; elapsed is its age.
func NextPollTick(tick, elapsed time.Duration) time.Duration {
	switch {
	case elapsed < time.Duration(pollFastWindow)*tick:
		return tick
	case elapsed < time.Duration(pollMediumWindow)*tick:
		return time.Duration(pollMediumTick) * tick
	default:
		return time.Duration(pollSlowTick) * tick
	}
}

// NextPollBackoff returns the gap after a failed poll; attempt is the failure
// count so far, 0 for the first. A server-sent retryAfter wins but shares the
// cap, so a hostile value cannot park the loop until the deadline.
func NextPollBackoff(tick time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	maxDelay := time.Duration(PollMaxBackoffTick) * tick
	if retryAfter > 0 {
		return min(retryAfter, maxDelay)
	}
	delay := tick
	for range attempt {
		delay *= time.Duration(pollBackoffFactor)
		if delay >= maxDelay {
			return maxDelay
		}
	}
	return delay
}
