//go:build !windows

package selfupdate

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockOpenFlags asks for no more access than the lock needs. flock takes an
// exclusive lock on a read-only descriptor all the same, and asking for write
// access would be refused on a lock file another account created under its own
// umask. O_NOFOLLOW is the sudo guard: root opening this path would otherwise
// follow a link an unprivileged process planted there.
const lockOpenFlags = os.O_RDONLY | os.O_CREATE | unix.O_NOFOLLOW

func lockFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrUpdateInProgress
	}
	return err
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
