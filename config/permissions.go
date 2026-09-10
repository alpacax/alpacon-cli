package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/alpacax/alpacon-cli/utils"
)

var (
	errSymlink = errors.New("symbolic link")
	// Separates a refusal that must stop the read from one a warning covers: a
	// read-only mount leaves a file this CLI still wrote, while a foreign owner
	// or a non-regular file means something else prepared the path.
	errUnsafeConfigFile = errors.New("unsafe config file")
	warnedUnrestricted  sync.Map
)

func restrictFileMode(file *os.File, allowed os.FileMode) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return restrictOpenFileMode(file, info, allowed, file.Chmod)
}

// restrictOpenFileMode narrows a handle to allowed, deciding from the info the
// caller already read: every guard that runs before the chmod sees the same
// inode state, so one fstat covers them all. The stats inside the chmod
// callback and the read-back below are the ones that need their own look.
func restrictOpenFileMode(file *os.File, info os.FileInfo, allowed os.FileMode, chmod func(os.FileMode) error) error {
	// Defense in depth rather than a live branch: every caller returns on GOOS
	// before reaching here, so nothing on Windows arrives at this line today.
	// It stays because the next caller might not, and a chmod on Windows would
	// flip the read-only attribute instead of narrowing anything.
	if runtime.GOOS == "windows" {
		return nil
	}
	perm := info.Mode().Perm() & allowed
	if perm == info.Mode().Perm() {
		return nil
	}
	if err := chmod(perm); err != nil {
		// A filesystem can refuse a mode it cannot represent while already
		// granting no other account anything—Linux vfat answers EPERM for any
		// mode outside the mount's fmask. Hold the refusal to the same standard
		// as the read-back below rather than reporting an exposure that is not
		// there; the device id fails closed on this error, so a mount whose
		// fmask is already narrow would otherwise never yield an identifier.
		kept, statErr := file.Stat()
		if statErr != nil || kept.Mode().Perm()&^allowed&0077 != 0 {
			return err
		}
		return nil
	}
	// A filesystem without permission bits, FAT above all, accepts the chmod and keeps the mode.
	kept, err := file.Stat()
	if err != nil {
		return err
	}
	// Only the bits that hand another account access matter here; a surviving
	// owner execute bit exposes nothing and FAT hands one out on every file.
	if kept.Mode().Perm()&^allowed&0077 != 0 {
		return fmt.Errorf("%s kept mode %04o after a chmod to %04o", file.Name(), kept.Mode().Perm(), perm)
	}
	return nil
}

// isSymlinkErrno reports whether an open failed because O_NOFOLLOW met a
// symbolic link. Which errno says so is the platform's own decision, so the list
// lives beside the other per-OS pieces rather than being assumed here.
func isSymlinkErrno(err error) bool {
	for _, errno := range symlinkErrnos {
		if errors.Is(err, errno) {
			return true
		}
	}
	return false
}

// wrapSymlinkErr names the refusal behind that errno so a caller can tell it
// from a filesystem failure, and returns anything else unchanged. O_NOFOLLOW is
// what produces it: following the link would let anyone who can write in the
// config directory aim the chmod at a file they do not own, and the mask only
// ever takes access away.
func wrapSymlinkErr(path string, err error) error {
	if isSymlinkErrno(err) {
		// Both wrapped: this is the neck every open error in the package passes
		// through, and dropping the errno would silently misclassify the next
		// condition someone sorts on.
		return fmt.Errorf("%s is a %w, so its target is left alone: %w", path, errSymlink, err)
	}
	return err
}

// refuseUnsafeConfigFile rejects a handle nothing should read, judging from the
// info the caller already read. A named pipe at the path is the reason for the
// first check: O_NONBLOCK keeps the open from parking, but says nothing about
// the read behind it—os.NewFile hands a non-blocking descriptor to the poller,
// and io.ReadAll then waits there for a writer that never arrives.
func refuseUnsafeConfigFile(file *os.File, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: %w", file.Name(), errUnsafeConfigFile)
	}
	if err := refuseForeignOwner(file, info); err != nil {
		return fmt.Errorf("%w: %w", err, errUnsafeConfigFile)
	}
	return nil
}

// openToNarrow opens what path itself names. O_NONBLOCK is what keeps a named
// pipe at the path from parking the open until a writer arrives, which would
// hang every command before it printed a thing.
func openToNarrow(path string) (*os.File, error) {
	file, err := openNoFollow(path)
	return file, wrapSymlinkErr(path, err)
}

// openNarrowedConfigFile narrows a file inside the config directory to 0600 and
// hands back the handle it narrowed, so a caller that must not be fooled reads
// the very inode this checked instead of resolving the path a second time.
//
// The file is reached through a handle on its directory rather than by its own
// path, because O_NOFOLLOW guards the last component alone: a symbolic link
// standing in for the config directory would otherwise be walked straight
// through, however firmly the directory's own narrowing refused it.
func openNarrowedConfigFile(path string) (*os.File, error) {
	if runtime.GOOS == "windows" {
		return openNoFollow(path)
	}
	dirPath := filepath.Dir(path)
	// The link is followed here, unlike where the directory's own mode is
	// narrowed: pointing ~/.alpacon at a dotfiles tree is a setup this supports,
	// and refusing it would leave that installation with no device id at all
	// while the same link keeps handing LoadConfig the refresh token. What the
	// refusal is for is aiming a chmod at a link someone else planted, and every
	// guard on the file itself still runs inside the directory it resolves to.
	dir, err := openConfigDirectory(dirPath)
	// errors.Is and not os.IsPermission throughout this file: these errors are
	// wrapped, and the os helpers unwrap only the error types the os package
	// defines, so they stop matching the moment an open is wrapped once more.
	if errors.Is(err, fs.ErrPermission) {
		// A directory that permits traversal but not reads still hands out a
		// search-only handle, which is all openat asks of it.
		dir, err = openSearchableDirectory(dirPath)
	}
	if err != nil {
		// Name the file that could not be reached as well as what blocked it: a
		// warning that quotes only the directory sends the reader to chmod a
		// path whose mode is not the one being complained about.
		return nil, fmt.Errorf("cannot reach %s: %w", path, err)
	}
	defer func() { _ = dir.Close() }()
	file, err := openNoFollowIn(dir, filepath.Base(path))
	if err != nil {
		return nil, wrapSymlinkErr(path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	// Always, unlike the hard-link guard below: this decides whether the handle
	// may be read from, not just whether it may be chmod'ed.
	if err = refuseUnsafeConfigFile(file, info); err != nil {
		_ = file.Close()
		return nil, err
	}
	// The guard sits on the chmod rather than ahead of it, so a file already
	// within the mask is accepted without it: publishing the device id leaves a
	// second name on the new file for as long as its temporary copy lives.
	if err = restrictOpenFileMode(file, info, 0600, func(mode os.FileMode) error {
		if err := refuseHardLinked(file); err != nil {
			return err
		}
		return file.Chmod(mode)
	}); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func restrictConfigDirectoryMode(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := openToNarrow(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// A directory that allows traversal but not reads has no ordinary handle, so
	// its mode has to be read from the path. Granting no other account anything
	// already satisfies the mask, and the platform fallbacks refuse a directory
	// whose owner holds no execute bit.
	if errors.Is(err, fs.ErrPermission) {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() {
			return fmt.Errorf("config directory is not a directory: %s", path)
		}
		if info.Mode().Perm()&0077 == 0 {
			return nil
		}
		return restrictSearchableDirectory(path)
	}
	if errors.Is(err, errSymlink) {
		// Already says what is wrong; naming the open on top of it reads as a
		// filesystem failure rather than a refusal.
		return err
	}
	if err != nil {
		return fmt.Errorf("failed to open config directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	info, err := dir.Stat()
	if err != nil {
		return fmt.Errorf("failed to inspect config directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("config directory is not a directory: %s", path)
	}
	return restrictOpenFileMode(dir, info, 0700, dir.Chmod)
}

// warnUnrestricted reports a mode this process could not narrow and carries on.
// A read-only mount, a directory owned by another account, and a filesystem
// without permission bits all refuse the chmod, and giving up there would take
// down a setup that reads its credentials perfectly well.
func warnUnrestricted(what string, err error) {
	if err == nil {
		return
	}
	warnOnce(what, err, "could not restrict %s permissions: %v. Another local account may be able to read your credentials while that stands.", what, err)
}

// warnOnce speaks the first time a subject fails for a given reason and stays
// quiet after that. A single command loads the config several times, and a mode
// this process cannot change will not have changed in between—but the reason is
// part of the key, so one condition cannot silence a different one.
func warnOnce(subject string, cause error, format string, args ...any) {
	if _, warned := warnedUnrestricted.LoadOrStore(subject+": "+cause.Error(), true); warned {
		return
	}
	utils.CliWarning(format, args...)
}
