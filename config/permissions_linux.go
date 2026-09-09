package config

import (
	"os"

	"golang.org/x/sys/unix"
)

func restrictSearchableDirectory(path string) error {
	file, err := os.OpenFile(path, unix.O_PATH|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return restrictOpenFileMode(file, 0700, func(mode os.FileMode) error {
		// O_PATH cannot be fchmod'ed; dot still names the opened directory.
		return unix.Fchmodat(int(file.Fd()), ".", uint32(mode), 0)
	})
}
