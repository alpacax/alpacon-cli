package utils

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// stderrMidLine is true while the last byte written through TrackedStderr was not a newline.
var stderrMidLine atomic.Bool

var (
	stderrMu     sync.Mutex
	stderrSealed bool // set by sealStderr; TrackedStderr writes nothing afterwards
)

type stderrTracker struct{}

func (stderrTracker) Write(p []byte) (int, error) {
	stderrMu.Lock()
	defer stderrMu.Unlock()
	if stderrSealed {
		return len(p), nil
	}
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

// sealStderr waits out a TrackedStderr write in flight and drops every later one, so a
// goroutine still running cannot print past the envelope an exiting process writes last.
func sealStderr() {
	stderrMu.Lock()
	defer stderrMu.Unlock()
	stderrSealed = true
}

// StderrForChild returns os.Stderr itself, so the child still sees a terminal, and treats
// the line as left open, since the CLI cannot see what the child writes.
func StderrForChild() *os.File {
	stderrMidLine.Store(true)
	return os.Stderr
}
