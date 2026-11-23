package kube

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// A first Ctrl+C reaches the command at once, even while its notice cannot be
// written, so a stderr that blocks does not hold up the cancellation. stop
// still returns only once the notice is out: the command's caller takes
// stderr back after it returns.
// Serial: watchInterrupt registers for the process's own signals.
func TestWatchInterruptReportsAtOnceAndStopWaitsForTheNotice(t *testing.T) {
	release := make(chan struct{})
	var written atomic.Bool
	ios, _, _ := utils.TestIOStreams()
	ios.ErrOut = writerFunc(func(p []byte) (int, error) {
		<-release
		written.Store(true)
		return len(p), nil
	})
	watch := watchInterrupt(ios, "Interrupted.", func() { t.Error("a first Ctrl+C must not quit") })

	watch.signals <- os.Interrupt
	select {
	case <-watch.first:
	case <-time.After(5 * time.Second):
		close(release)
		watch.stop()
		require.FailNow(t, "the interrupt waited on the notice's write")
	}
	assert.True(t, watch.fired.Load())

	go func() {
		// Late enough that a stop which does not wait has long returned.
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	watch.stop()

	assert.True(t, written.Load(), "stop returned while the notice was still being written")
}
