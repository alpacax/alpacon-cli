package utils

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Off a TTY the spinner prints one static line instead of animating, so there
// is no frame for a warning to land on—and the escape would be noise in a
// redirected log. The spinner is built inside the capture so stderr is already
// the pipe when it reads the terminal.
func TestSpinner_DoesNotArmTheClearOffATerminal(t *testing.T) {
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
