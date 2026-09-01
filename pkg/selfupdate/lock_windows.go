//go:build windows

package selfupdate

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const lockOpenFlags = os.O_RDWR | os.O_CREATE

const lockFileBytes = 1 // The byte range LockFileEx takes: Windows has no whole-file form, and a range every caller names identically is exclusive all the same.

func lockFile(file *os.File) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, lockFileBytes, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrUpdateInProgress
	}
	return err
}

func unlockFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, lockFileBytes, 0, new(windows.Overlapped))
}
