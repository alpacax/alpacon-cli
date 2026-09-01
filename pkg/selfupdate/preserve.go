package selfupdate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

// staleTempAge is how long a utils.SaveStreamAtomic temp file must have sat
// before the sweep will take it. The update lock is no help: 'alpacon cp' and
// 'alpacon package download' write the same name shape into whatever directory
// they were pointed at, and take no lock. Age is what separates a killed run's
// litter from a download still streaming—a leftover is permanent, so collecting
// it a day late costs nothing.
const staleTempAge = 24 * time.Hour

const (
	preservedSuffix = "old"
	stagedSuffix    = "staged"
)

var osRename = os.Rename

func PreservedName(path string, now time.Time) string {
	return timestampedName(path, preservedSuffix, now)
}

func StagedName(path string, now time.Time) string { // A run killed mid-copy leaves the staging file behind, and a fixed name would have the next run write into it.
	return timestampedName(path, stagedSuffix, now)
}

func timestampedName(path, suffix string, now time.Time) string {
	return fmt.Sprintf("%s.%s.%d", path, suffix, now.UnixNano())
}

// RestorePreserved falls back to a copy because a rename cannot cross a
// filesystem boundary, and because on Windows the target may be held open by
// something that took it between the failure and this call.
func RestorePreserved(preservedPath, targetPath string) error {
	if err := osRename(preservedPath, targetPath); err == nil {
		return nil
	}

	if err := copyPreserved(preservedPath, targetPath); err != nil {
		return err
	}
	return os.Remove(preservedPath)
}

// copyPreserved exists to bound the source handle. Windows refuses to delete a
// file anything still has open, so a deferred close in RestorePreserved would
// make the remove that follows it fail on the platform this path is for.
func copyPreserved(preservedPath, targetPath string) error {
	source, err := os.Open(preservedPath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()

	_, err = utils.SaveStreamAtomic(targetPath, source, 0755)
	return err
}

// SweepPreserved clears what an earlier update could not delete: on Windows the
// preserved copy is the executable that was running then, and a staging or
// utils.SaveStreamAtomic temp file outlives a run killed while writing it.
func SweepPreserved(dir, binaryName string) int {
	removed := 0
	for _, entry := range sweepableEntries(dir, binaryName) {
		if err := os.Remove(filepath.Join(dir, entry.Name())); err == nil {
			removed++
		}
	}
	return removed
}

// hasSweepableEntry lets a caller find out before taking the lock, which
// outlives the run as a file in the install directory.
func hasSweepableEntry(dir, binaryName string) bool {
	return len(sweepableEntries(dir, binaryName)) > 0
}

func sweepableEntries(dir, binaryName string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(binaryName) + `\.(` + preservedSuffix + `|` + stagedSuffix + `)\.\d+$`)
	var matched []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if pattern.MatchString(entry.Name()) || staleReplacementTemp(entry) {
			matched = append(matched, entry)
		}
	}
	return matched
}

func staleReplacementTemp(entry os.DirEntry) bool {
	if !utils.IsReplacementTempName(entry.Name()) {
		return false
	}
	info, err := entry.Info()
	return err == nil && time.Since(info.ModTime()) > staleTempAge
}

func copyFile(sourcePath, destPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()

	info, err := source.Stat()
	if err != nil {
		return err
	}

	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	// A truncated copy in the install directory looks exactly like a preserved
	// binary worth restoring.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(destPath)
		}
	}()

	// O_CREATE only asks; the umask narrows. A rollback renames this copy over
	// the install, so a trimmed mode would silently re-permission it.
	chmodErr := dest.Chmod(info.Mode().Perm())
	_, copyErr := io.Copy(dest, source)
	closeErr := dest.Close()
	if copyErr != nil {
		return copyErr
	}
	if chmodErr != nil {
		return chmodErr
	}
	if closeErr != nil {
		return closeErr
	}
	cleanup = false
	return nil
}
