package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextPollTick(t *testing.T) {
	t.Parallel()
	base := time.Second
	tests := []struct {
		name    string
		elapsed time.Duration
		want    time.Duration
	}{
		{name: "base tick while the command is young", elapsed: 0, want: base},
		{name: "still base at the end of the fast window", elapsed: 9 * base, want: base},
		{name: "widens once the fast window closes", elapsed: 10 * base, want: 5 * base},
		{name: "holds through the medium window", elapsed: 59 * base, want: 5 * base},
		{name: "widest once the medium window closes", elapsed: 60 * base, want: 10 * base},
		{name: "stays widest", elapsed: 3000 * base, want: 10 * base},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NextPollTick(base, tt.elapsed))
		})
	}
}

func TestNextPollBackoff(t *testing.T) {
	t.Parallel()
	base := time.Second
	tests := []struct {
		name       string
		attempt    int
		retryAfter time.Duration
		want       time.Duration
	}{
		{name: "first failure keeps the base tick", attempt: 0, want: base},
		{name: "doubles per consecutive failure", attempt: 1, want: 2 * base},
		{name: "still doubling", attempt: 3, want: 8 * base},
		{name: "capped once doubling passes the cap", attempt: 6, want: 60 * base},
		{name: "stays capped", attempt: 20, want: 60 * base},
		{name: "server's Retry-After wins over the schedule", attempt: 0, retryAfter: 3 * base, want: 3 * base},
		{name: "Retry-After wins even when it is shorter", attempt: 5, retryAfter: 3 * base, want: 3 * base},
		{name: "Retry-After is capped too", attempt: 0, retryAfter: 3000 * base, want: 60 * base},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NextPollBackoff(base, tt.attempt, tt.retryAfter))
		})
	}
}

func TestCappedBackoff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		base    time.Duration
		attempt int
		limit   time.Duration
		want    time.Duration
	}{
		{name: "download retry before any doubling", base: 250 * time.Millisecond, attempt: 0, limit: time.Second, want: 250 * time.Millisecond},
		{name: "download retry doubled once", base: 250 * time.Millisecond, attempt: 1, limit: time.Second, want: 500 * time.Millisecond},
		{name: "download retry reaches the cap", base: 250 * time.Millisecond, attempt: 2, limit: time.Second, want: time.Second},
		{name: "download retry far past the cap", base: 250 * time.Millisecond, attempt: 50, limit: time.Second, want: time.Second},
		{name: "websh reconnect doubled past the cap", base: time.Second, attempt: 4, limit: 15 * time.Second, want: 15 * time.Second},
		{name: "websh reconnect just under the cap", base: time.Second, attempt: 3, limit: 15 * time.Second, want: 8 * time.Second},
		{name: "gap fill on its first no-progress attempt", base: 300 * time.Millisecond, attempt: 0, limit: 5 * time.Second, want: 300 * time.Millisecond},
		{name: "gap fill one doubling under the cap", base: 300 * time.Millisecond, attempt: 4, limit: 5 * time.Second, want: 4800 * time.Millisecond},
		{name: "gap fill doubled past the cap", base: 300 * time.Millisecond, attempt: 5, limit: 5 * time.Second, want: 5 * time.Second},
		{name: "poll backoff reaches its sixty tick cap", base: time.Second, attempt: 6, limit: 60 * time.Second, want: 60 * time.Second},
		{name: "a base already over the limit is capped", base: 5 * time.Second, attempt: 0, limit: time.Second, want: time.Second},
		{name: "a base over the limit stays capped", base: 5 * time.Second, attempt: 3, limit: time.Second, want: time.Second},
		{name: "a negative attempt keeps the base", base: 300 * time.Millisecond, attempt: -1, limit: 5 * time.Second, want: 300 * time.Millisecond},
		{name: "a zero base stays zero", base: 0, attempt: 10, limit: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CappedBackoff(tt.base, tt.attempt, tt.limit))
		})
	}
}

func TestThrottleBudget_ExtendsUntilTheDurationRunsOut(t *testing.T) {
	t.Parallel()
	budget := NewThrottleBudget(10 * time.Second)
	deadline := time.Now()

	for i := range 3 {
		moved, extended := budget.Extend(deadline, 4*time.Second)
		require.True(t, extended, "extension %d is granted while the budget is not yet spent", i)
		assert.Equal(t, deadline.Add(4*time.Second), moved)
		deadline = moved
	}

	moved, extended := budget.Extend(deadline, 4*time.Second)
	assert.False(t, extended, "12s already spent against a 10s budget")
	assert.Equal(t, deadline, moved)
}

func TestThrottleBudget_ExtendsUntilTheCountRunsOut(t *testing.T) {
	t.Parallel()
	budget := NewThrottleBudget(time.Hour)
	deadline := time.Now()

	for i := range PollMaxThrottleExtensions {
		var extended bool
		deadline, extended = budget.Extend(deadline, time.Millisecond)
		require.True(t, extended, "extension %d is within the count cap", i)
	}

	_, extended := budget.Extend(deadline, time.Millisecond)
	assert.False(t, extended)
}

func TestThrottleBudget_WarnsOnceUntilReset(t *testing.T) {
	t.Parallel()
	budget := NewThrottleBudget(time.Minute)

	assert.True(t, budget.ShouldWarn(), "the first throttled poll of a stretch warns")
	assert.False(t, budget.ShouldWarn(), "a stretch warns once, not per poll")

	budget.Reset()
	assert.True(t, budget.ShouldWarn(), "progress restores the warning")
}

// Reset clears the spent duration and the extension count as well as the warning
// flag. A Reset that only cleared the flag would leave a wait that saw progress
// unable to buy a single extension for the throttles that followed.
func TestThrottleBudget_ResetRestoresTheAllowance(t *testing.T) {
	t.Parallel()

	t.Run("duration", func(t *testing.T) {
		t.Parallel()
		budget := NewThrottleBudget(10 * time.Second)
		deadline := time.Now()
		_, spent := budget.Extend(deadline, 10*time.Second)
		require.True(t, spent, "the whole budget is spendable in one grant")
		_, extended := budget.Extend(deadline, time.Second)
		require.False(t, extended, "the budget is spent")

		budget.Reset()

		_, extended = budget.Extend(deadline, time.Second)
		assert.True(t, extended, "progress must refund the spent duration")
	})

	t.Run("count", func(t *testing.T) {
		t.Parallel()
		budget := NewThrottleBudget(time.Hour)
		deadline := time.Now()
		for range PollMaxThrottleExtensions {
			deadline, _ = budget.Extend(deadline, time.Millisecond)
		}
		_, extended := budget.Extend(deadline, time.Millisecond)
		require.False(t, extended, "the count cap is reached")

		budget.Reset()

		_, extended = budget.Extend(deadline, time.Millisecond)
		assert.True(t, extended, "progress must refund the extension count")
	})
}
