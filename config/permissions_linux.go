package config

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

var symlinkErrnos = []error{syscall.ELOOP}

// openSearchableDirectory hands back a handle on a directory that permits
// traversal but not reads. O_PATH asks for neither.
func openSearchableDirectory(path string) (*os.File, error) {
	return os.OpenFile(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
}

func restrictSearchableDirectory(path string) error {
	file, err := openSearchableDirectory(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return restrictOpenFileMode(file, 0700, func(mode os.FileMode) error {
		// O_PATH cannot be fchmod'ed; dot still names the opened directory.
		if err := unix.Fchmodat(int(file.Fd()), ".", uint32(mode), 0); err != nil {
			// A bare errno would reach the user as "permission denied" alone.
			return &os.PathError{Op: "fchmodat", Path: path, Err: err}
		}
		return nil
	})
}
