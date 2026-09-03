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

// Extend grows deadline by delay when the budget allows it, and reports whether
// it did. An extension is granted while the time spent so far is still under
// limit and the count is still under PollMaxThrottleExtensions.
//
// The grant that carries the total past limit is taken whole rather than
// trimmed, since a clipped grant would leave the poll no time to serve the wait
// the throttled server itself just asked for. That overshoot is one delay wide,
// which the caller caps at PollMaxBackoffTick times its base tick, and no
// further extension follows it.
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

// WarnThrottled prints the rate-limit notice the first time a throttled stretch
// calls it, and stays quiet for the rest of that stretch. Every wait loop that
// spends a budget shows the same line, so the wording lives here rather than at
// each call site. CliWarning steps off a running spinner's line itself, so a
// caller animating one need not stop it first.
func (b *ThrottleBudget) WarnThrottled(delay time.Duration) {
	if b.ShouldWarn() {
		CliWarning("rate limited by the server, retrying in %s", delay)
	}
}

// Reset clears the spent budget, extension count, and warning flag—called when
// the poll sees progress, so a throttle late in a long wait is not charged
// against an earlier, unrelated one.
func (b *ThrottleBudget) Reset() {
	b.spent = 0
	b.extensions = 0
	b.warned = false
}

// ThrottleCeiling is the longest a throttled wait may run: its own timeout, one
// timeout of extensions, and the overshoot of the grant that crosses the budget,
// which Extend takes whole. Exported for the wait loops' own tests the way
// PollMaxBackoffTick is—a wait that refills its budget has no ceiling at all.
func ThrottleCeiling(timeout, tick time.Duration) time.Duration {
	return 2*timeout + time.Duration(PollMaxBackoffTick)*tick
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
