package config

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows has no O_NOFOLLOW, so nothing here reports a refused symbolic link.
var symlinkErrnos []error

// Windows has neither O_NOFOLLOW nor openat. openNoFollow and openConfigForRead
// are reached on every run and open the file plainly; the rest are not, because
// the callers that would use them return on GOOS first.
func openNoFollow(path string) (*os.File, error) { return openConfigForRead(path) }

func openConfigForRead(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

func openConfigDirectory(path string) (*os.File, error) { return os.Open(path) }

func openNoFollowIn(dir *os.File, name string) (*os.File, error) { return nil, os.ErrInvalid }

func refuseHardLinked(*os.File) error { return nil }

func refuseForeignOwner(*os.File, os.FileInfo) error { return nil }
