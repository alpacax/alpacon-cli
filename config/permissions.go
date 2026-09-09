package config

import (
	"fmt"
	"os"
	"runtime"
)

func restrictFileMode(file *os.File, allowed os.FileMode) error {
	return restrictOpenFileMode(file, allowed, file.Chmod)
}

func restrictOpenFileMode(file *os.File, allowed os.FileMode, chmod func(os.FileMode) error) error {
	if runtime.GOOS == "windows" {
		// Windows chmod changes the read-only attribute, not Unix access permissions.
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	perm := info.Mode().Perm() & allowed
	if perm == info.Mode().Perm() {
		return nil
	}
	return chmod(perm)
}

func restrictConfigDirectoryMode(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect config directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("config directory is not a directory: %s", path)
	}
	// Already private directories may allow traversal without directory reads.
	if info.Mode().Perm()&0077 == 0 {
		return nil
	}
	dir, err := os.Open(path)
	if os.IsPermission(err) {
		if err = restrictSearchableDirectory(path); err != nil {
			return fmt.Errorf("failed to restrict config directory permissions: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to open config directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err = restrictFileMode(dir, 0700); err != nil {
		return fmt.Errorf("failed to restrict config directory permissions: %w", err)
	}
	return nil
}
