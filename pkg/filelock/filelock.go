// Package filelock takes an advisory lock the kernel drops when this process
// exits, however it exits. A lock named only by the file's own contents would
// need an age rule to survive a killed process, and no age works: shorter than
// the slowest holder and it evicts a live one, longer and a killed one strands
// the next run.
package filelock

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrLocked reports that another holder has the lock, as opposed to a lock
// file that could not be opened at all. The holder need not be another
// process: a second Acquire in this one is refused the same way.
var ErrLocked = errors.New("the lock is already held")

// Lock is one held lock; Release gives it up.
type Lock struct {
	file *os.File
}

// Acquire creates the directory and the lock file as needed and takes a non-blocking exclusive
// lock on it. perm applies only to a file this call creates.
func Acquire(path string, perm os.FileMode) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, lockOpenFlags, perm)
	if err != nil {
		return nil, err
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
