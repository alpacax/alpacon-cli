package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
