package config

import (
	"os"

	"golang.org/x/sys/unix"
)

// O_SEARCH is available since macOS 13 but is not exposed by x/sys.
const directorySearch = 0x40000000 | unix.O_DIRECTORY

func restrictSearchableDirectory(path string) error {
	file, err := os.OpenFile(path, directorySearch, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return restrictFileMode(file, 0700)
}
