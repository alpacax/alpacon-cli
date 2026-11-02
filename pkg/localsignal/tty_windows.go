//go:build windows

package localsignal

import (
	"io"
	"os"
)

func openTTY() (io.WriteCloser, error) {
	// Read access lets Go recognize the console and write Unicode text.
	return os.OpenFile("CONOUT$", os.O_RDWR, 0)
}
