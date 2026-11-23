package kube

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// The first Ctrl+C's notice is written before the command can see the
// interrupt. Seen first, the command may finish and its caller restore
// stderr while the notice is still being written to it.
// Serial: watchInterrupt registers for the process's own signals.
func TestWatchInterruptWritesTheNoticeBeforeReportingTheInterrupt(t *testing.T) {
	var watch *interruptWatch
	var reportedFirst atomic.Bool
	wrote := make(chan struct{}, 1)
	ios, _, _ := utils.TestIOStreams()
	ios.ErrOut = writerFunc(func(p []byte) (int, error) {
		select {
		case <-watch.first:
			reportedFirst.Store(true)
		default:
		}
		if watch.fired.Load() {
			reportedFirst.Store(true)
		}
		select {
		case wrote <- struct{}{}:
		default:
		}
		return len(p), nil
	})
	watch = watchInterrupt(ios, "Interrupted.", func() { t.Error("a first Ctrl+C must not quit") })
	defer watch.stop()

	watch.signals <- os.Interrupt
	<-watch.first
	<-wrote

	assert.True(t, watch.fired.Load())
	assert.False(t, reportedFirst.Load(), "the interrupt was visible to the command before its notice was written")
}
