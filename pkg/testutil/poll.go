package testutil

import (
	"sync"
	"time"
)

// PollRecorder collects the time of each poll a stubbed server sees, so a test
// can assert the schedule that produced them rather than the poll count alone. A
// count is a weak witness: a widened schedule and a fixed tick can land on the
// same number. Under testing/synctest the times come off the bubble's fake clock,
// which makes the gaps exact.
type PollRecorder struct {
	mu    sync.Mutex
	times []time.Time
}

// Record marks one poll as having arrived now. Safe to call from a handler
// goroutine.
func (r *PollRecorder) Record() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.times = append(r.times, time.Now())
}

// WidestGap returns the longest interval between consecutive polls, or 0 when
// fewer than two arrived. Only a schedule that widens with age can produce a gap
// above its own base tick.
func (r *PollRecorder) WidestGap() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	var widest time.Duration
	for i := 1; i < len(r.times); i++ {
		widest = max(widest, r.times[i].Sub(r.times[i-1]))
	}
	return widest
}
