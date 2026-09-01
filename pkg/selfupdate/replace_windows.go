//go:build windows

package selfupdate

import (
	"errors"
	"os"
	"time"
)

// ReplaceBinary renames the target aside instead of overwriting it: Windows
// refuses to overwrite or delete a running executable but allows renaming it.
// The copy runs first, so a Ctrl-C during it leaves the install path untouched.
// The gap is between the two renames—the restore covers a second rename that
// reports failure, but a process killed there leaves the install path empty with
// only the preserved copy beside it. That copy is swept on the next update,
// since it can stay locked for as long as this process lives.
func ReplaceBinary(targetPath, newBinaryPath string) error {
	now := time.Now()
	stagedPath := StagedName(targetPath, now)
	if err := copyFile(newBinaryPath, stagedPath); err != nil {
		_ = os.Remove(stagedPath)
		return err
	}

	preservedPath := PreservedName(targetPath, now)
	if err := os.Rename(targetPath, preservedPath); err != nil {
		_ = os.Remove(stagedPath)
		return err
	}
	if err := os.Rename(stagedPath, targetPath); err != nil {
		_ = os.Remove(stagedPath)
		return errors.Join(err, RestorePreserved(preservedPath, targetPath))
	}
	return nil
}
