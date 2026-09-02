package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrUpdateInProgress = errors.New("another alpacon update is already running")

	ErrLockUnavailable = errors.New("cannot open the update lock") // One shape for both ways the lock can fail to open, and it names the lock, which neither underlying message does.
)

// Lock is an advisory lock the kernel drops when this process exits, however it
// exits. A lock named only by the file's own contents would need an age rule to
// survive a killed process, and no age works: shorter than a slow download and
// it evicts a live update, longer and a killed one strands the next run.
type Lock struct {
	file *os.File
}

// LockPath puts the lock beside the binary it guards. In the home directory it
// would guard nothing: sudo moves HOME to /root on Linux, so the root run and
// the user run would take two different locks over one install path—and where
// sudo keeps HOME, root would leave a lock file the user can no longer open.
func LockPath(executablePath string) string {
	return filepath.Join(filepath.Dir(executablePath), ".alpacon-update.lock")
}

func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLockUnavailable, err)
	}

	// 0666, not 0600: the file holds nothing and the kernel is what makes the
	// lock exclusive. A mode only its creator can open would shut every other
	// account out of a group-writable install directory, and one
	// 'sudo alpacon update' would leave a root-owned lock behind for good.
	file, err := os.OpenFile(path, lockOpenFlags, 0666)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLockUnavailable, err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

// Release drops the lock and leaves the file behind. Unlinking it would let a
// process that opened it before the unlink and one that creates it after hold
// locks on two different files at one path, which is the race the lock exists
// to prevent.
func (l *Lock) Release() error {
	err := unlockFile(l.file)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	return err
}
