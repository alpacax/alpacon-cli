package config

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func renameConfig(from, to string) error {
	return retryConfigRename(func() error { return os.Rename(from, to) }, func(err error) bool {
		return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
	})
}
