//go:build unix

package localsignal

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

type unixTTY struct {
	fd           int
	isForeground func(int) (bool, error)
}

func (t *unixTTY) Write(p []byte) (int, error) {
	foreground, err := t.isForeground(t.fd)
	if err != nil {
		return 0, err
	}
	if !foreground {
		return 0, os.ErrPermission
	}
	// os.File can wait for writability after EAGAIN; a banner must be dropped instead.
	n, err := unix.Write(t.fd, p)
	return max(n, 0), err
}

func (t *unixTTY) Close() error { return unix.Close(t.fd) }

func openTTY() (io.WriteCloser, error) {
	return openTTYFile("/dev/tty", ttyForeground)
}

func openTTYFile(name string, isForeground func(int) (bool, error)) (io.WriteCloser, error) {
	fd, err := unix.Open(name, unix.O_WRONLY|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	foreground, err := isForeground(fd)
	if err != nil || !foreground {
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		return nil, os.ErrPermission
	}
	return &unixTTY{fd: fd, isForeground: isForeground}, nil
}

func ttyForeground(fd int) (bool, error) {
	group, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	return err == nil && group == unix.Getpgrp(), err
}
