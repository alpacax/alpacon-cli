//go:build !windows

package localsignal

import (
	"errors"
	"os"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
	"golang.org/x/sys/unix"
)

func openSignalFile(path string, create bool) (*os.File, error) {
	flags := os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("signal state must be a regular file")
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func lockSignalFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return filelock.ErrLocked
	}
	return err
}

func unlockSignalFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
