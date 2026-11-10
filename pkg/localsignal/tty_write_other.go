//go:build !windows

package localsignal

import "io"

func writeTTY(open func() (io.WriteCloser, error), banner string) error {
	return writeTTYNow(open, banner)
}
