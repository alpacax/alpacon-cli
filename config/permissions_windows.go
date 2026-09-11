package config

import "os"

// Windows has no O_NOFOLLOW, so nothing here reports a refused symbolic link.
var symlinkErrnos []error

// Windows has neither O_NOFOLLOW nor openat. openNoFollow and openConfigForRead
// are reached on every run and open the file plainly; the rest are not, because
// the callers that would use them return on GOOS first.
func openNoFollow(path string) (*os.File, error) { return os.Open(path) }

func openConfigForRead(path string) (*os.File, error) { return os.Open(path) }

func openConfigDirectory(path string) (*os.File, error) { return os.Open(path) }

func openNoFollowIn(dir *os.File, name string) (*os.File, error) { return nil, os.ErrInvalid }

func refuseHardLinked(*os.File) error { return nil }

func refuseForeignOwner(*os.File, os.FileInfo) error { return nil }
