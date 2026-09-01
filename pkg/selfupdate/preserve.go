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
// Callers hold the update lock, which is what keeps a live write safe.
func SweepPreserved(dir, binaryName string) int {
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(binaryName) + `\.(` + preservedSuffix + `|` + stagedSuffix + `)\.\d+$`)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!pattern.MatchString(name) && !utils.IsReplacementTempName(name)) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	return removed
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
	_, copyErr := io.Copy(dest, source)
	closeErr := dest.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
