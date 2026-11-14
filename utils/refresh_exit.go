package utils

import (
	"os"
	"sync"
	"time"
)

var tokenRefreshes = struct {
	sync.Mutex
	pending map[chan struct{}]struct{}
}{pending: make(map[chan struct{}]struct{})}

// BeginTokenRefresh keeps process exit from interrupting a rotated token's save.
func BeginTokenRefresh() func() {
	done := make(chan struct{})
	tokenRefreshes.Lock()
	tokenRefreshes.pending[done] = struct{}{}
	tokenRefreshes.Unlock()
	return func() {
		tokenRefreshes.Lock()
		delete(tokenRefreshes.pending, done)
		close(done)
		tokenRefreshes.Unlock()
	}
}

// WaitForTokenRefreshes gives grants already in flight one grant deadline to finish.
func WaitForTokenRefreshes() {
	tokenRefreshes.Lock()
	pending := make([]chan struct{}, 0, len(tokenRefreshes.pending))
	for done := range tokenRefreshes.pending {
		pending = append(pending, done)
	}
	tokenRefreshes.Unlock()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for _, done := range pending {
		select {
		case <-done:
		case <-timer.C:
			return
		}
	}
}

// Exit lets a refresh save its rotated token before terminating the process.
func Exit(code int) { WaitForTokenRefreshes(); os.Exit(code) }

// ProtectRefreshOnSignal lets an active grant save before a terminating signal is re-raised.
func ProtectRefreshOnSignal() func() { return cleanupOnSignal(func() {}) }
