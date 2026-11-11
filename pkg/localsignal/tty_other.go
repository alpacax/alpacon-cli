//go:build !unix && !windows

package localsignal

import (
	"io"
	"os"
)

func openTTY() (io.WriteCloser, error) { return nil, os.ErrNotExist }
