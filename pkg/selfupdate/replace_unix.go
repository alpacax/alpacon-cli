//go:build !windows

package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

var writeReplacement = utils.SaveStreamAtomic

func ReplaceBinary(targetPath, newBinaryPath string) error { // The 0755 handed to utils.SaveStreamAtomic is only a fallback for a target that does not exist: an installed binary keeps its own permission mode.
	if err := refuseSymlinkTarget(targetPath); err != nil { // Before the copy, so a swapped target is not read at all.
		return err
	}
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

	if err := refuseSymlinkTarget(targetPath); err != nil { // Again: the download between the two checks is the window a planted link needs.
		return errors.Join(err, RestorePreserved(preservedPath, targetPath))
	}

	if _, err := writeReplacement(targetPath, source, 0755); err != nil {
		return errors.Join(err, RestorePreserved(preservedPath, targetPath))
	}

	_ = os.Remove(preservedPath)
	return nil
}

// utils.SaveStreamAtomic walks links when it writes, so a target swapped for a
// link sends this write—running as root—wherever it points. Two Lstats narrow
// the window to the write itself; closing it needs a writer that opens
// O_NOFOLLOW, which SaveStreamAtomic deliberately does not.
func refuseSymlinkTarget(targetPath string) error {
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace %s: it became a symlink after the update started", targetPath)
	}
	return nil
}

// A warning, not a chmod: re-permissioning something the operator set up is not
// the CLI's call, but a binary any local account can swap is worth saying aloud.
func warnWorldWritableInstall(targetPath string) {
	if info, err := os.Stat(targetPath); err == nil && groupOrOtherWritable(info) {
		utils.CliWarning("%s is writable by group or other (%04o) and the update keeps that mode; any local account can then replace the binary you run. Close it with 'chmod go-w %s'.",
			targetPath, info.Mode().Perm(), targetPath)
	}

	// The directory matters more than the file. Replacing a file is a rename,
	// which needs no permission on the file at all—so a 0755 root-owned binary
	// in a 0775 directory is swappable by anyone in that group. Sticky is the
	// exemption: it stops a non-owner from renaming over somebody else's file.
	installDir := filepath.Dir(targetPath)
	info, err := os.Stat(installDir)
	if err != nil || info.Mode()&os.ModeSticky != 0 || !groupOrOtherWritable(info) {
		return
	}
	utils.CliWarning("%s is writable by group or other (%04o); replacing a file there needs no permission on the file itself, so any local account can swap the binary you run. Close it with 'chmod go-w %s'.",
		installDir, info.Mode().Perm(), installDir)
}

func groupOrOtherWritable(info os.FileInfo) bool {
	const groupOtherWrite = os.FileMode(0022)
	return info.Mode().Perm()&groupOtherWrite != 0
}
