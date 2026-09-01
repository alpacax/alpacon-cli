package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
)

// stagingPerm is the mode a replacement is written under, never a final mode:
// it hides partial content from other local accounts until the write completes.
const stagingPerm = 0600

var replacementTempPattern = regexp.MustCompile(`^\.alpacon-\d+-\d+-\d+\.tmp$`)

// IsReplacementTempName reports whether name is a createReplacementTempFile
// staging file, which only a killed process leaves behind. A live write wears
// the same name, so sweep one only while holding the directory's writer lock.
func IsReplacementTempName(name string) bool {
	return replacementTempPattern.MatchString(name)
}

func SaveFile(fileName string, data []byte) error {
	_, err := saveStream(fileName, bytes.NewReader(data))
	return err
}

func saveStream(fileName string, r io.Reader) (int64, error) {
	dir := filepath.Dir(fileName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create directories: %w", err)
	}

	file, err := os.Create(fileName)
	if err != nil {
		return 0, fmt.Errorf("failed to create file: %w", err)
	}

	written, copyErr := io.Copy(file, r)
	closeErr := file.Close()
	if copyErr != nil {
		return written, fmt.Errorf("failed to write file: %w", copyErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("failed to close file: %w", closeErr)
	}

	return written, nil
}

// SaveStreamAtomic writes r to fileName through a temp file in the same
// directory, then renames it into place. The mode is decided when the write
// starts: a destination already on disk keeps its own mode, so a write does not
// re-permission a file the user set up, and one that is not there yet gets
// newFilePerm minus the umask.
func SaveStreamAtomic(fileName string, r io.Reader, newFilePerm os.FileMode) (int64, error) {
	targetName, err := resolveWritePath(fileName)
	if err != nil {
		return 0, err
	}

	dir := filepath.Dir(targetName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create directories: %w", err)
	}

	finalPerm := newFilePerm
	replacing := false
	if info, err := os.Stat(targetName); err == nil {
		if info.IsDir() {
			return 0, fmt.Errorf("destination is a directory: %s", targetName)
		}
		finalPerm = info.Mode().Perm()
		replacing = true
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("failed to access file: %w", err)
	}
	warnKeptWiderMode(fileName, replacing, finalPerm, newFilePerm)

	// A replacement is staged narrow and widened once the content is complete, so
	// a download that dies mid-stream never leaves its partial content readable
	// to anyone else. A new file cannot be staged the same way: it would need a
	// chmod that bypasses the umask, and a caller passing 0666 is asking for the
	// umask to apply.
	createPerm := finalPerm
	if replacing {
		createPerm = stagingPerm
	}

	file, err := createReplacementTempFile(dir, createPerm)
	if err != nil {
		return 0, err
	}
	tempName := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = file.Close()
			_ = os.Remove(tempName)
		}
	}()

	written, copyErr := io.Copy(file, r)
	// Staging cost the destination its own mode, so restore it before the rename.
	// Through the handle: a path-based chmod here could be pointed at another
	// file by anyone able to swap tempName for a symlink.
	var chmodErr error
	if copyErr == nil && replacing {
		chmodErr = file.Chmod(finalPerm)
	}
	closeErr := file.Close()
	if copyErr != nil {
		return written, fmt.Errorf("failed to write file: %w", copyErr)
	}
	if chmodErr != nil {
		return written, fmt.Errorf("failed to set temp file permissions: %w", chmodErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("failed to close file: %w", closeErr)
	}

	if err := replaceFile(tempName, targetName); err != nil {
		return written, fmt.Errorf("failed to replace file: %w", err)
	}
	cleanup = false

	return written, nil
}

// warnKeptWiderMode signals when the kept mode leaves the content readable
// beyond what a fresh download would be (a private key over a 0644 file). A
// warning, not a chmod—re-permissioning a file the user set up is not the
// CLI's call (#326).
//
// !replacing is redundant at today's only call site (keptPerm equals
// newFilePerm there, which no comparison satisfies) and stays so the guard does
// not rest on that invariant. The message names fileName rather than the
// symlink-resolved targetName: removing the name the user typed is what gets
// them a fresh owner-only file.
func warnKeptWiderMode(fileName string, replacing bool, keptPerm, newFilePerm os.FileMode) {
	if !replacing || !keptModeIsWider(keptPerm, newFilePerm) {
		return
	}
	CliWarning("%s already existed at %04o and kept that mode; a new file would have been at most %04o. Remove it first for owner-only permissions.",
		fileName, keptPerm, newFilePerm)
}

// keptModeIsWider reports whether keptPerm leaves the content readable to
// accounts newFilePerm would shut out. Windows has no Unix modes—os.Stat
// synthesizes 0666 for every file without the read-only attribute—so the
// comparison would fire on every overwrite there, and removing the file is no
// remedy for an ACL anyway.
//
// newFilePerm is the mode a new file is requested at, before the umask narrows
// it, so a restrictive umask can only silence a warning that was due—never
// invent one.
func keptModeIsWider(keptPerm, newFilePerm os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	const groupOtherRead = os.FileMode(0044)
	return keptPerm&groupOtherRead != 0 && newFilePerm&groupOtherRead == 0
}

// resolveWritePath walks symlinks to the final target so atomic replace
// operates on the underlying file rather than the symlink itself.
func resolveWritePath(fileName string) (string, error) {
	targetName := fileName
	for i := 0; i < 255; i++ {
		info, err := os.Lstat(targetName)
		if os.IsNotExist(err) {
			return targetName, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed to access file: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return targetName, nil
		}

		nextName, err := os.Readlink(targetName)
		if err != nil {
			return "", fmt.Errorf("failed to resolve symlink: %w", err)
		}
		if !filepath.IsAbs(nextName) {
			nextName = filepath.Join(filepath.Dir(targetName), nextName)
		}
		targetName = nextName
	}
	return "", errors.New("too many symlinks while resolving write path")
}

func createReplacementTempFile(dir string, perm os.FileMode) (*os.File, error) {
	for i := 0; i < 100; i++ {
		name := filepath.Join(dir, fmt.Sprintf(".alpacon-%d-%d-%d.tmp", os.Getpid(), time.Now().UnixNano(), i))
		file, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file: %w", err)
		}
		return file, nil
	}
	return nil, errors.New("failed to create temp file after repeated attempts")
}
