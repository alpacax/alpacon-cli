// Package testutil holds helpers shared by tests across packages.
package testutil

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
)

// CaptureStdout returns what fn writes to os.Stdout.
func CaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	stop := redirect(t, &os.Stdout)
	fn()
	return stop()
}

// CaptureOutput returns what fn writes to os.Stdout and os.Stderr.
func CaptureOutput(t *testing.T, fn func()) (stdout string, stderr string) {
	t.Helper()
	stopOut := redirect(t, &os.Stdout)
	stopErr := redirect(t, &os.Stderr)
	fn()
	return stopOut(), stopErr()
}

// redirect points target at a pipe and returns a func giving back what was
// written to it. The returned func is idempotent, so t.Cleanup can call it on
// the panicking path: leaving the pipe installed would swallow the output of
// every later test in the package.
func redirect(t *testing.T, target **os.File) func() string {
	t.Helper()
	old := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	*target = w

	// Drain concurrently, or fn blocks once it writes past the pipe buffer.
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	var (
		once      sync.Once
		collected string
	)
	stop := func() string {
		once.Do(func() {
			*target = old
			_ = w.Close()
			collected = <-done
			// io.Copy has returned by now, so both ends are done with the pipe.
			_ = r.Close()
		})
		return collected
	}
	t.Cleanup(func() { stop() })
	return stop
}
