//go:build !linux && !darwin

package config

import "os"

func restrictSearchableDirectory(path string) error {
	return &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
}
