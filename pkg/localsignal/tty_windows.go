//go:build windows

package localsignal

import (
	"io"
	"os"
	"time"
)

func openTTY() (io.WriteCloser, error) {
	// Read access lets Go recognize the console and write Unicode text.
	return os.OpenFile("CONOUT$", os.O_RDWR, 0)
}

func writeTTY(open func() (io.WriteCloser, error), banner string) error {
	done := make(chan error, 1)
	go func() { done <- writeTTYNow(open, banner) }()
	select {
	case err := <-done:
		return err
	case <-time.After(200 * time.Millisecond):
		return os.ErrDeadlineExceeded
	}
}
