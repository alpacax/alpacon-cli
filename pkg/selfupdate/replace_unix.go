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
