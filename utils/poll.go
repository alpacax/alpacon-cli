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

	// PollMaxBackoffTick is exported for api/event's throttle-budget test, which
	// needs the cap as a constant expression to state its own deadline.
	PollMaxBackoffTick = 60

	// MaxConsecutivePollFailures bounds a wait's ride through failed polls: a run
	// of them means the CLI cannot reach the server, and saying so beats
	// reporting an outcome nobody read.
	MaxConsecutivePollFailures = 5
)

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
