package selfupdate

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
)

var (
	ErrUpdateInProgress = errors.New("another alpacon update is already running")

	// ErrLockUnavailable names the lock, which no underlying error does.
	ErrLockUnavailable = errors.New("cannot open the update lock")
)

// Lock is the advisory lock that keeps two updates off one install path. The
// kernel drops it when this process exits, however it exits.
type Lock struct {
	lock *filelock.Lock
}

// LockPath puts the lock beside the binary it guards. In the home directory it
// would guard nothing: sudo moves HOME to /root on Linux, so the root run and
// the user run would take two different locks over one install path—and where
// sudo keeps HOME, root would leave a lock file the user can no longer open.
func LockPath(executablePath string) string {
	return filepath.Join(filepath.Dir(executablePath), ".alpacon-update.lock")
}

func AcquireLock(path string) (*Lock, error) {
	// 0666, not 0600: the kernel makes the lock exclusive. A creator-only mode would shut other
	// accounts out of a group-writable install directory, and one 'sudo alpacon update' would
	// leave a root-owned lock behind for good.
	lock, err := filelock.Acquire(path, 0666)
	if errors.Is(err, filelock.ErrLocked) {
		return nil, ErrUpdateInProgress
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLockUnavailable, err)
	}
	return &Lock{lock: lock}, nil
}

// Release drops the lock and leaves the file behind for the next update.
func (l *Lock) Release() error {
	return l.lock.Release()
}
