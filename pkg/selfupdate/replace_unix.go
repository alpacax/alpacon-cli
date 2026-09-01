//go:build !windows

package selfupdate

import (
	"errors"
	"os"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

var saveStream = utils.SaveStreamAtomic

func ReplaceBinary(targetPath, newBinaryPath string) error { // The 0755 handed to utils.SaveStreamAtomic is only a fallback for a target that does not exist: an installed binary keeps its own permission mode.
	warnWorldWritableInstall(targetPath)

	preservedPath := PreservedName(targetPath, time.Now())
	if err := copyFile(targetPath, preservedPath); err != nil {
		return err
	}

	source, err := os.Open(newBinaryPath)
	if err != nil {
		_ = os.Remove(preservedPath)
		return err
	}
	defer func() { _ = source.Close() }()

	if _, err := saveStream(targetPath, source, 0755); err != nil {
		return errors.Join(err, RestorePreserved(preservedPath, targetPath))
	}

	_ = os.Remove(preservedPath)
	return nil
}

// A warning, not a chmod: re-permissioning a file the operator set up is not
// the CLI's call, but a binary any local account can swap is worth saying aloud.
func warnWorldWritableInstall(targetPath string) {
	const groupOtherWrite = os.FileMode(0022)

	info, err := os.Stat(targetPath)
	if err != nil || info.Mode().Perm()&groupOtherWrite == 0 {
		return
	}
	utils.CliWarning("%s is writable by group or other (%04o) and the update keeps that mode; any local account can then replace the binary you run. Close it with 'chmod go-w %s'.",
		targetPath, info.Mode().Perm(), targetPath)
}
