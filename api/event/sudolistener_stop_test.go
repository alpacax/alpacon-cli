package event

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type gatedWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.entered)
		<-w.release
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *gatedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestSudoListener_StopWaitsForInFlightNoticeAndSilencesLaterOnes(t *testing.T) {
	t.Parallel()
	// Given a notice blocked mid-write
	w := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	sl := NewSudoListener(nil, "my-server", "session-1")
	sl.out = w
	printed := make(chan struct{})
	go func() {
		sl.printf("in-flight\n")
		close(printed)
	}()
	<-w.entered

	// When Stop is called
	stopped := make(chan struct{})
	go func() {
		sl.Stop()
		close(stopped)
	}()

	// Then it does not return until the write finishes
	select {
	case <-stopped:
		t.Fatal("Stop returned while a notice was still being written")
	case <-time.After(50 * time.Millisecond):
	}
	close(w.release)
	require.Eventually(t, func() bool {
		select {
		case <-stopped:
			return true
		default:
			return false
		}
	}, 5*time.Second, time.Millisecond)
	<-printed

	// And nothing is written once Stop has returned
	sl.printf("late\n")
	assert.Equal(t, "in-flight\n", w.String())
}
