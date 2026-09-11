//go:build !windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

const narrowFlags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, narrowFlags, 0)
}

// openConfigDirectory opens the config directory as a base for openat, following
// a symbolic link on it. Only the chmod of the directory's own mode refuses one.
func openConfigDirectory(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|unix.O_DIRECTORY, 0)
}

// openConfigForRead opens a config file for reading. O_NONBLOCK is what leaves
// the caller a chance to reject a named pipe instead of parking on the open.
func openConfigForRead(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

func openNoFollowIn(dir *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, narrowFlags|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: filepath.Join(dir.Name(), name), Err: err}
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir.Name(), name)), nil
}

// refuseHardLinked rejects a file that answers to more than one name. O_NOFOLLOW
// says nothing about hard links, so a link planted in a directory this process
// could not narrow would otherwise aim the chmod at an inode that also lives
// outside the config directory—and macOS lets any account link a file it can
// merely read.
//
// This one reads the handle again rather than taking the caller's info: it runs
// inside the chmod callback, and the link it refuses can be planted after the
// guards ahead of it have looked.
func refuseHardLinked(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	sys, err := statT(file, info)
	if err != nil {
		return err
	}
	if sys.Nlink != 1 {
		return fmt.Errorf("%s answers to %d names, so it may live outside the config directory", file.Name(), sys.Nlink)
	}
	return nil
}

// refuseForeignOwner rejects a handle on a file this process does not own. A
// chmod would fail on it regardless; the point is the read that follows. A file
// another account owns is one another account can rewrite, and under a sudo
// that keeps HOME this is what stops an unprivileged user's own file from
// standing in as root's device id—where mode bits alone say nothing, because
// root can read anything.
//
// The uid this process runs as is named alongside the file's own, because the
// two ordinary ways to arrive here are `sudo -E` and a bind-mounted config
// directory in a container, and a message that quotes only the file sends the
// reader off to delete credentials that are working.
func refuseForeignOwner(file *os.File, info os.FileInfo) error {
	sys, err := statT(file, info)
	if err != nil {
		return err
	}
	if int(sys.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is owned by uid %d, but this process runs as uid %d", file.Name(), sys.Uid, os.Geteuid())
	}
	return nil
}

// statT reaches the Unix stat behind an os.FileInfo. Refusing rather than
// returning nil is the point: both callers exist to turn something away, and a
// guard that answers "allowed" when it could not look is worse than one that
// says it could not look. No Unix reaches this—os.File always carries a
// syscall.Stat_t—so nothing working is turned away by it either.
func statT(file *os.File, info os.FileInfo) (*syscall.Stat_t, error) {
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("cannot read the owner and link count of %s on %s", file.Name(), runtime.GOOS)
	}
	return sys, nil
}
