//go:build !linux && !darwin

package config

import (
	"fmt"
	"os"
	"runtime"
)

func openSearchableDirectory(path string) (*os.File, error) {
	return nil, unsupportedSearchableDirectory(path)
}

func restrictSearchableDirectory(path string) error {
	return unsupportedSearchableDirectory(path)
}

func unsupportedSearchableDirectory(path string) error {
	return fmt.Errorf("%s needs owner read permission to narrow: opening a directory for search alone is unsupported on %s: %w", path, runtime.GOOS, os.ErrPermission)
}
