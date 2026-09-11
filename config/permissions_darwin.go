package config

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// O_SEARCH is available since macOS 13 but is not exposed by x/sys.
const directorySearch = 0x40000000 | unix.O_DIRECTORY

var symlinkErrnos = []error{syscall.ELOOP}

// openSearchableDirectory hands back a handle on a directory that permits
// traversal but not reads.
func openSearchableDirectory(path string) (*os.File, error) {
	file, err := os.OpenFile(path, directorySearch|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.EINVAL) {
		return nil, fmt.Errorf("%w: narrowing a directory without owner read permission needs macOS 13 or later", err)
	}
	return file, err
}

func restrictSearchableDirectory(path string) error {
	file, err := openSearchableDirectory(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return restrictFileMode(file, 0700)
}
