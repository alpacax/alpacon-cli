package utils

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Off a TTY the spinner prints one static line instead of animating, so there
// is no frame for a warning to land on—and the break would only add a blank
// line to a redirected log. The spinner is built inside the capture so stderr
// is already the pipe when it reads the terminal.
func TestSpinner_DoesNotArmTheLineBreakOffATerminal(t *testing.T) {
	stderr := captureStderr(t, func() {
		s := NewSpinner("Downloading id_rsa...")
		s.Start()
		assert.Equal(t, int32(0), activeSpinners.Load())
		s.Stop()
	})

	assert.Equal(t, int32(0), activeSpinners.Load())
	assert.Contains(t, stderr, "Downloading id_rsa...")
}

// spinnerRunning reads the flag under the same lock Start and Stop take, so the
// race detector sees an ordered read.
func spinnerRunning(s *Spinner) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// A spinner animating the line output is about to land on gets drawn over that
// output, and Stop then erases the whole line. StopWriter is what orders the
// two, so this pins the handover: the spinner is gone before the first byte,
// and every byte still reaches the wrapped writer.
func TestSpinner_StopWriterRetiresTheSpinnerBeforeTheFirstByte(t *testing.T) {
	var out bytes.Buffer

	captureStderr(t, func() {
		s := NewSpinner("Waiting for approval...")
		s.Start()
		w := s.StopWriter(&out)

		_, err := w.Write(nil)
		require.NoError(t, err)
		assert.True(t, spinnerRunning(s), "an empty write is not output, so the spinner stays")

		_, err = w.Write([]byte("first"))
		require.NoError(t, err)
		assert.False(t, spinnerRunning(s), "the spinner must be gone before output lands")

		_, err = w.Write([]byte(" and second"))
		require.NoError(t, err)
	})

	assert.Equal(t, "first and second", out.String())
}

// Only the animating branch counts itself, and off a TTY—which is every go test
// run—Start returns before reaching it. Forcing enabled is what lets a test see
// that branch at all, and the pairing it pins is what CliWarning's line break
// reads: a dropped increment, or a decrement moved ahead of the goroutine's
// exit, would otherwise leave the whole suite green.
func TestSpinner_AnimatingBranchPairsTheActiveCount(t *testing.T) {
	captureStderr(t, func() {
		s := NewSpinner("Waiting for approval...")
		s.enabled = true
		s.interval = time.Millisecond

		require.Equal(t, int32(0), activeSpinners.Load())
		s.Start()
		assert.Equal(t, int32(1), activeSpinners.Load(), "an animating spinner arms the line break")
		s.Stop()
		assert.Equal(t, int32(0), activeSpinners.Load(), "and Stop disarms it")
	})
}

// Stop closes stopCh and doneCh; a Start that reuses them launches a goroutine
// whose select falls straight through the closed stopCh and then closes doneCh a
// second time. Off a TTY Start never reaches the goroutine, so enabled is
// forced here as well—no test that leaves it alone can see the panic.
func TestSpinner_RestartsAfterStop(t *testing.T) {
	captureStderr(t, func() {
		s := NewSpinner("Waiting for approval...")
		s.enabled = true
		s.interval = time.Millisecond

		require.Equal(t, int32(0), activeSpinners.Load())
		s.Start()
		s.Stop()

		s.Start()
		assert.Equal(t, int32(1), activeSpinners.Load(), "a restarted spinner arms the line break again")
		s.Stop()
		assert.Equal(t, int32(0), activeSpinners.Load(), "and its Stop disarms it")
		assert.False(t, spinnerRunning(s), "Stop must leave the spinner stopped")
	})
}

// time.NewTicker panics on a non-positive period where the sleep it replaced
// took one happily, and the panic would fire on the spinner's own goroutine—far
// from the caller and fatal to the process. No caller can set that today, so
// without the fallback this test does not fail, it takes the test binary down.
func TestSpinner_NonPositiveIntervalFallsBackToTheDefault(t *testing.T) {
	captureStderr(t, func() {
		s := NewSpinner("Waiting for approval...")
		s.enabled = true
		s.interval = 0

		s.Start()
		s.Stop()

		assert.Equal(t, int32(0), activeSpinners.Load(), "the goroutine ran to its own exit rather than dying on the ticker")
	})
}

// Start and Stop each guard the fields, but the goroutine waits on what it was
// handed for its whole run—a restart that swapped the fields under it would
// leave it listening to a channel nobody closes. Only -race can see that, and
// only if a second goroutine drives the restart: StopWriter hands Stop to
// whoever writes the output, which need not be the goroutine that restarts.
func TestSpinner_RestartAndStopRaceOnTheSameSpinner(t *testing.T) {
	captureStderr(t, func() {
		s := NewSpinner("Waiting for approval...")
		s.enabled = true
		s.interval = time.Millisecond

		var wg sync.WaitGroup
		wg.Add(2)
		for range 2 {
			go func() {
				defer wg.Done()
				for range 50 {
					s.Start()
					s.Stop()
				}
			}()
		}
		wg.Wait()

		s.Stop()
		assert.Equal(t, int32(0), activeSpinners.Load(), "every Start that armed the line break is paired with a Stop")
	})
}
