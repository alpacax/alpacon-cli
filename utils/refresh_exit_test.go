package utils

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

// Serial: observes the process-wide refresh tracker.
func TestWaitForTokenRefreshes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		delay, want time.Duration
	}{
		{"finishes before exit", time.Second, time.Second},
		{"bounded wait", 20 * time.Second, 15 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				finish := BeginTokenRefresh()
				done := make(chan struct{})
				go func() { time.Sleep(tc.delay); finish(); close(done) }()
				start := time.Now()
				WaitForTokenRefreshes()
				assert.Equal(t, tc.want, time.Since(start))
				<-done
			})
		})
	}
}
