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

func writeTTY(open func() (io.WriteCloser, error), banner string) {
	done := make(chan struct{})
	go func() { writeTTYNow(open, banner); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
	}
}
