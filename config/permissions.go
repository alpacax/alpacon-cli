// Package config keeps the CLI's credentials on disk and narrows what the rest
// of the machine can do with them.
//
// # What is narrowed
//
// `~/.alpacon` is held at 0700 and the files inside it at 0600. Anything found
// wider is narrowed in place before it is read or written rather than only at
// the moment it is created, because a file that was already wide when this
// CLI arrived is exactly the one worth closing. This file decides what to
// narrow; permissions_unix.go holds the guards it refuses on, and the
// platform-specific opens live in permissions_darwin.go and
// permissions_linux.go.
//
// # Handles, not paths
//
// Each chmod is bound to an open handle rather than to a path, so nothing can
// be swapped in between the look and the change. Two path-based reads are
// left, and neither decides a mode by itself: the pre-check in
// restrictConfigDirectoryMode that asks whether a directory permitting
// traversal but not reads has anything to narrow at all—safe because the
// platform helper behind it re-derives the mode from its own handle and only
// ever ANDs with 0700—and narrowToPublishedFileMode's os.Stat in device.go,
// which reads the mode a rename is about to stand in for and intersects it, so
// it can only take bits away.
//
// # Links
//
// openToNarrow refuses a symbolic link instead of following one: the target
// could be a file the user does not own, and the same run under sudo would
// take access away from whatever the link named. Which errno says a link was
// met is the platform's own decision, so symlinkErrnos is declared per OS
// (ELOOP on Linux and macOS, EMLINK and EFTYPE alongside it on the BSDs,
// nothing on Windows) rather than assumed.
//
// Reaching a file inside the directory is the one place a link is followed.
// openConfigDirectory opens ~/.alpacon without O_NOFOLLOW, because pointing it
// at a dotfiles tree is a supported setup and refusing it would leave that
// installation with no device id while the same link kept handing LoadConfig
// the refresh token. Only the directory's own chmod refuses the link, which is
// the attack this guards: aiming a chmod at a link someone else planted. Every
// guard on the file itself still runs inside the directory the link resolves
// to, and the file is reached with openat on that handle, so O_NOFOLLOW covers
// the last component the link could not.
//
// A refused chmod is not by itself an exposure, so neither the file nor the
// directory warns on a link alone. What the link resolves to is read for its
// mode—the handle LoadConfig already holds for the file, a fresh O_NOFOLLOW open
// for the directory—and the warning is kept back unless that mode grants another
// account access. A dotfiles tree usually keeps its config.json at 0600, and a
// notice on every command there teaches the reader to skip the one that matters.
//
// The chmod separately refuses a file that answers to more than one name,
// because O_NOFOLLOW says nothing about hard links and macOS lets any account
// link a file it can merely read. That guard sits inside the chmod callback
// rather than ahead of it, so a file already within the mask passes:
// publishing the device id leaves a second name on the new file for as long as
// its temporary copy lives.
//
// # What a caller must not read past
//
// refuseUnsafeConfigFile rejects anything that is not a regular file, since
// O_NONBLOCK keeps the open off a named pipe but says nothing about the read
// behind it—os.NewFile hands a non-blocking descriptor to the poller and
// io.ReadAll then waits there forever—and it rejects a handle on a file this
// process does not own, because mode bits say nothing there: root can read
// anything, and a sudo that keeps HOME would otherwise take an unprivileged
// user's own file as root's device id. Both carry errUnsafeConfigFile, and
// that is the one narrowing failure LoadConfig refuses rather than warns
// about, since a config file another account owns names the host the CLI talks
// to and decides whether its certificate is checked. LoadConfig reads the very
// handle this narrowed, and falls back to reopening the path only for a
// symbolic link, which is refused a chmod but not a read—that fallback runs
// the same refusal on what the link resolves to.
//
// # Best effort everywhere else
//
// The mode is read back after a chmod that reported success, since a
// filesystem without permission bits answers the call and keeps the mode; only
// bits that hand another account access count as a failure, because macOS
// reports every file on a FAT volume as 0700. A chmod that fails outright is
// held to the same standard, which is what keeps a Linux vfat mount whose
// fmask already grants no other account access from being read as an exposure.
// Everything else is best effort—a read-only mount, a directory another
// account owns, and a modeless filesystem all refuse the chmod—so
// warnUnrestricted warns once per subject and cause and the command carries on
// rather than dying over a mode it cannot change. ~/.alpacon/device_id is the
// exception in the other direction: it fails closed, because an MFA presence
// proof binds to whatever identifier readDeviceID returns, so a handle it
// could not protect is treated as no identifier at all and the server falls
// back to an IP fingerprint.
//
// # Windows
//
// All of it is Unix only. Windows chmod changes the read-only attribute rather
// than access permissions, so nothing is repaired there, the two modes above
// are not enforced, and the symlink, hard-link and owner refusals are
// stubs—the device id is not protected on Windows either.
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

// grantsOtherAccounts reports whether a mode hands any account but the owner
// access. The owner's own bits are not part of the question: macOS reports every
// file on a FAT volume as 0700, and that exposes nothing.
func grantsOtherAccounts(perm os.FileMode) bool {
	return perm&0077 != 0
}

// symlinkTargetGrantsOtherAccounts reports whether what a link resolves to hands
// another account access. A link is refused a chmod but not a read, so whether
// that refusal is worth telling the user about is a question about the target
// rather than about the link—and keeping the config in a dotfiles tree, where
// the file is usually already 0600, is a setup this CLI supports.
//
// It answers true when it cannot look. A mode nothing could read is not one
// anything may vouch for, and the caller's warning is the safe way to be wrong.
func symlinkTargetGrantsOtherAccounts(path string) bool {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return true
	}
	// O_NOFOLLOW on the resolved path: EvalSymlinks has walked every link
	// already, so a link still standing at the last component was planted
	// between the two calls.
	file, err := openToNarrow(target)
	if err != nil {
		return true
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return true
	}
	return grantsOtherAccounts(info.Mode().Perm())
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
		if statErr != nil || grantsOtherAccounts(kept.Mode().Perm()&^allowed) {
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
	if grantsOtherAccounts(kept.Mode().Perm() &^ allowed) {
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
		if !grantsOtherAccounts(info.Mode().Perm()) {
			return nil
		}
		return restrictSearchableDirectory(path)
	}
	if errors.Is(err, errSymlink) {
		// Pointing ~/.alpacon at a dotfiles tree is supported, and the chmod is
		// refused on the link rather than on what it names—so nothing is exposed
		// unless the target's own mode says so. Reporting the link either way
		// warned on every command for a setup that was fine, which is how a user
		// learns to read past the warning that matters.
		if !symlinkTargetGrantsOtherAccounts(path) {
			return nil
		}
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
