package utils

import (
	"io"
	"os"
	"sync/atomic"
)

// stderrMidLine is true while the last byte written through TrackedStderr was not a newline.
var stderrMidLine atomic.Bool

type stderrTracker struct{}

func (stderrTracker) Write(p []byte) (int, error) {
	n, err := os.Stderr.Write(p)
	if n > 0 {
		stderrMidLine.Store(p[n-1] != '\n')
	}
	return n, err
}

// TrackedStderr is os.Stderr as the process has it now, recording whether the last byte
// left the cursor mid-line, so the error envelope can start on its own line.
func TrackedStderr() io.Writer {
	return stderrTracker{}
}
