package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

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

	perm := newFilePerm
	replacing := false
	if info, err := os.Stat(targetName); err == nil {
		if info.IsDir() {
			return 0, fmt.Errorf("destination is a directory: %s", targetName)
		}
		perm = info.Mode().Perm()
		replacing = true
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("failed to access file: %w", err)
	}

	// A replacement is written owner-only and widened to the destination's mode
	// only once the content is complete, so a download that dies mid-stream
	// never leaves its partial content readable to anyone else.
	createPerm := perm
	if replacing {
		createPerm = 0600
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
	closeErr := file.Close()
	if copyErr != nil {
		return written, fmt.Errorf("failed to write file: %w", copyErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("failed to close file: %w", closeErr)
	}

	// The umask stripped bits from what O_CREATE requested, so an existing mode
	// needs a chmod to survive the replacement intact.
	if replacing {
		if err := os.Chmod(tempName, perm); err != nil {
			return written, fmt.Errorf("failed to set temp file permissions: %w", err)
		}
	}

	if err := replaceFile(tempName, targetName); err != nil {
		return written, fmt.Errorf("failed to replace file: %w", err)
	}
	cleanup = false

	return written, nil
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
