//go:build !windows

package config

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestrictConfigDirectoryModeRejectsANonDirectoryWithoutBlocking(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		skipAsRoot bool
		create     func(path string) error
	}{
		// An O_RDONLY open of a named pipe parks until a writer arrives, which
		// would hang every command before it printed a thing.
		{"named pipe", false, func(path string) error { return syscall.Mkfifo(path, 0600) }},
		{"regular file", false, func(path string) error { return os.WriteFile(path, []byte("{}"), 0644) }},
		// Only a denied open reaches the branch that reads the mode from the path.
		{"unreadable regular file", true, func(path string) error { return os.WriteFile(path, []byte("{}"), 0000) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.skipAsRoot && os.Geteuid() == 0 {
				t.Skip("root bypasses file permission checks")
			}
			path := filepath.Join(t.TempDir(), ConfigFileDir)
			require.NoError(t, tc.create(path))
			done := make(chan error, 1)
			go func() { done <- restrictConfigDirectoryMode(path) }()
			select {
			case err := <-done:
				require.EqualError(t, err, "config directory is not a directory: "+path)
			case <-time.After(10 * time.Second):
				t.Error("restrictConfigDirectoryMode blocked instead of rejecting a non-directory")
			}
		})
	}
}

func TestNarrowingRefusesASymbolicLink(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		linkName string
		create   func(target string) error
		narrow   func(path string) error
	}{
		{"config file", ConfigFileName, func(target string) error { return os.WriteFile(target, []byte("{}"), 0644) }, narrowConfigFile},
		{"config directory", ConfigFileDir, func(target string) error { return os.Mkdir(target, 0755) }, restrictConfigDirectoryMode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			target := filepath.Join(dir, "victim")
			require.NoError(t, tc.create(target))
			before, err := os.Stat(target)
			require.NoError(t, err)
			link := filepath.Join(dir, tc.linkName)
			require.NoError(t, os.Symlink(target, link))
			require.ErrorIs(t, tc.narrow(link), errSymlink)
			after, err := os.Stat(target)
			require.NoError(t, err)
			assert.Equal(t, before.Mode().Perm(), after.Mode().Perm())
		})
	}
}

func TestGetOrCreateDeviceIDRefusesAnIdentifierItCouldNotNarrow(t *testing.T) {
	setupTestConfig(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ConfigFileDir)
	require.NoError(t, os.MkdirAll(dir, 0700))
	planted := filepath.Join(home, "planted")
	require.NoError(t, os.WriteFile(planted, []byte("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"), 0644))
	require.NoError(t, os.Symlink(planted, filepath.Join(dir, DeviceIDFileName)))
	id, err := GetOrCreateDeviceID()
	require.ErrorIs(t, err, errSymlink)
	require.ErrorContains(t, err, "cannot use the device id file")
	assert.Empty(t, id)
}

func TestRestrictConfigFileModeNarrowsThroughASymlinkedConfigDirectory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// Pointing ~/.alpacon at a dotfiles tree is a supported setup, so the link on
	// the directory is followed and the file inside it is still narrowed. Only
	// the directory's own mode is left alone, which TestNarrowingRefusesA
	// SymbolicLink covers.
	target := filepath.Join(home, "elsewhere")
	require.NoError(t, os.Mkdir(target, 0700))
	stored := filepath.Join(target, ConfigFileName)
	require.NoError(t, os.WriteFile(stored, []byte("{}"), 0600))
	require.NoError(t, os.Chmod(stored, 0644))
	link := filepath.Join(home, ConfigFileDir)
	require.NoError(t, os.Symlink(target, link))
	require.NoError(t, narrowConfigFile(filepath.Join(link, ConfigFileName)))
	info, err := os.Stat(stored)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestGetOrCreateDeviceIDSurvivesASymlinkedConfigDirectory(t *testing.T) {
	setupTestConfig(t)
	home := os.Getenv("HOME")
	// The identifier used to be lost outright here, which silently downgraded
	// every MFA presence check to a network fingerprint while the same link kept
	// handing LoadConfig the refresh token.
	target := filepath.Join(home, "dotfiles")
	require.NoError(t, os.Mkdir(target, 0700))
	require.NoError(t, os.Symlink(target, filepath.Join(home, ConfigFileDir)))

	created, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	assert.NotEmpty(t, created)
	info, err := os.Stat(filepath.Join(target, DeviceIDFileName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	reread, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	assert.Equal(t, created, reread)
}

func TestRestrictConfigFileModeRefusesAHardLink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ConfigFileDir)
	require.NoError(t, os.Mkdir(dir, 0700))
	// A hard link says nothing to O_NOFOLLOW, and macOS lets any account link a
	// file it can merely read.
	outside := filepath.Join(home, "outside")
	require.NoError(t, os.WriteFile(outside, []byte("{}"), 0600))
	require.NoError(t, os.Chmod(outside, 0644))
	require.NoError(t, os.Link(outside, filepath.Join(dir, ConfigFileName)))
	err := narrowConfigFile(filepath.Join(dir, ConfigFileName))
	require.ErrorContains(t, err, "answers to 2 names")
	info, err := os.Stat(outside)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestOpenNoFollowInStaysWithTheDirectoryItHolds(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	held := filepath.Join(home, ConfigFileDir)
	require.NoError(t, os.Mkdir(held, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(held, ConfigFileName), []byte("original"), 0600))
	dir, err := openToNarrow(held)
	require.NoError(t, err)
	defer func() { _ = dir.Close() }()
	// Swap the directory the path names, after the handle exists. Resolving the
	// path a second time would reach the replacement; openat cannot.
	replacement := filepath.Join(home, "replacement")
	require.NoError(t, os.Mkdir(replacement, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(replacement, ConfigFileName), []byte("swapped"), 0600))
	require.NoError(t, os.Rename(held, filepath.Join(home, "moved")))
	require.NoError(t, os.Rename(replacement, held))
	file, err := openNoFollowIn(dir, ConfigFileName)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, "original", string(data))
}

func TestRefuseForeignOwner(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		skipAsRoot bool
		// The root directory is owned by uid 0 on every Unix, which is the shape
		// the guard exists for: readable, and not ours.
		open      func(t *testing.T) string
		wantError string
	}{
		{"a directory owned by root", true, func(*testing.T) string { return "/" }, "is owned by uid 0"},
		{"a file this process created", false, func(t *testing.T) string {
			t.Helper()
			path := filepath.Join(t.TempDir(), ConfigFileName)
			require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
			return path
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.skipAsRoot && os.Geteuid() == 0 {
				t.Skip("root owns the file this borrows")
			}
			file, err := os.Open(tc.open(t))
			require.NoError(t, err)
			defer func() { _ = file.Close() }()
			info, err := file.Stat()
			require.NoError(t, err)
			if tc.wantError == "" {
				assert.NoError(t, refuseForeignOwner(file, info))
				return
			}
			assert.ErrorContains(t, refuseForeignOwner(file, info), tc.wantError)
		})
	}
}

func TestRefuseUnsafeConfigFileNamesWhatMustNotBeRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		skipAsRoot bool
		// /etc/hosts is a regular file owned by uid 0 and readable by everyone on
		// both Linux and macOS, which is the owner guard's shape: readable, ours
		// to open, and not ours to trust.
		open      func(t *testing.T) string
		wantError string
	}{
		{"a named pipe", false, func(t *testing.T) string {
			t.Helper()
			path := filepath.Join(t.TempDir(), ConfigFileName)
			require.NoError(t, syscall.Mkfifo(path, 0600))
			return path
		}, "is not a regular file"},
		{"a file owned by root", true, func(*testing.T) string { return "/etc/hosts" }, "is owned by uid 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.skipAsRoot && os.Geteuid() == 0 {
				t.Skip("root owns the file this borrows")
			}
			file, err := openConfigForRead(tc.open(t))
			require.NoError(t, err)
			defer func() { _ = file.Close() }()
			info, err := file.Stat()
			require.NoError(t, err)
			err = refuseUnsafeConfigFile(file, info)
			require.ErrorIs(t, err, errUnsafeConfigFile)
			assert.ErrorContains(t, err, tc.wantError)
		})
	}
}

func TestConfigReadsRefuseANamedPipeWithoutBlocking(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fileName string
		read     func() error
	}{
		// Both parked forever before: the narrowing open passes O_NONBLOCK, but
		// the read behind it waits on a writer that never arrives.
		{"config file", ConfigFileName, func() error { _, err := LoadConfig(); return err }},
		{"device id file", DeviceIDFileName, func() error { _, err := GetOrCreateDeviceID(); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupTestConfig(t)
			dir := filepath.Join(os.Getenv("HOME"), ConfigFileDir)
			require.NoError(t, os.MkdirAll(dir, 0700))
			require.NoError(t, syscall.Mkfifo(filepath.Join(dir, tc.fileName), 0600))
			done := make(chan error, 1)
			go func() { done <- tc.read() }()
			select {
			case err := <-done:
				require.ErrorContains(t, err, "is not a regular file")
			case <-time.After(10 * time.Second):
				t.Error("reading a named pipe blocked instead of being refused")
			}
		})
	}
}

func TestLoadConfigStillReadsThroughASymlinkedConfigFile(t *testing.T) {
	setupTestConfig(t)
	home := os.Getenv("HOME")
	require.NoError(t, saveConfig(&Config{Token: "test-token"}))
	// Keeping a config.json in a dotfiles tree is what the warn-and-continue
	// policy exists for: the link is refused a chmod, not a read.
	stored := filepath.Join(home, "dotfiles-config.json")
	configFile := filepath.Join(home, ConfigFileDir, ConfigFileName)
	require.NoError(t, os.Rename(configFile, stored))
	require.NoError(t, os.Symlink(stored, configFile))

	config, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "test-token", config.Token)
}

func TestRestrictConfigFileModeAcceptsAHardLinkedFileAlreadyWithinTheMask(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ConfigFileDir)
	require.NoError(t, os.Mkdir(dir, 0700))
	// The hard-link guard sits on the chmod, not ahead of it, because publishing
	// the device id leaves a second name on the new file while its temporary copy
	// lives. A file already at 0600 needs no chmod and must pass.
	outside := filepath.Join(home, "outside")
	require.NoError(t, os.WriteFile(outside, []byte("{}"), 0600))
	require.NoError(t, os.Link(outside, filepath.Join(dir, ConfigFileName)))
	assert.NoError(t, narrowConfigFile(filepath.Join(dir, ConfigFileName)))
}

func TestLoadConfigRefusesAConfigFileOwnedByAnotherAccount(t *testing.T) {
	setupTestConfig(t)
	dir := filepath.Join(os.Getenv("HOME"), ConfigFileDir)
	require.NoError(t, os.MkdirAll(dir, 0700))
	configFile := filepath.Join(dir, ConfigFileName)
	// A file another account owns is one another account can rewrite, and this
	// one names the host the CLI talks to and whether its certificate is checked.
	// Linking a root-owned file is how macOS lets an attacker leave one behind;
	// Linux refuses the link, and there the case has no local shape to build.
	if err := os.Link("/etc/hosts", configFile); err != nil || os.Geteuid() == 0 {
		t.Skip("cannot plant a file owned by another account here")
	}

	_, err := LoadConfig()
	require.ErrorIs(t, err, errUnsafeConfigFile)
	assert.ErrorContains(t, err, "is owned by uid 0")
}

func TestLoadConfigRefusesASymlinkToANamedPipe(t *testing.T) {
	setupTestConfig(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ConfigFileDir)
	require.NoError(t, os.MkdirAll(dir, 0700))
	// The narrowing warns about the link rather than following it, so the read is
	// the first look at what it resolves to.
	pipe := filepath.Join(home, "pipe")
	require.NoError(t, syscall.Mkfifo(pipe, 0600))
	require.NoError(t, os.Symlink(pipe, filepath.Join(dir, ConfigFileName)))

	done := make(chan error, 1)
	go func() { _, err := LoadConfig(); done <- err }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, errUnsafeConfigFile)
		require.ErrorContains(t, err, "is not a regular file")
	case <-time.After(10 * time.Second):
		t.Error("reading a symlinked named pipe blocked instead of being refused")
	}
}

func TestIsSymlinkErrnoReadsThePlatformList(t *testing.T) {
	t.Parallel()
	// The errno O_NOFOLLOW reports for a link is the platform's decision, so the
	// list is what this checks; the refusal itself is covered end to end by
	// TestNarrowingRefusesASymbolicLink on whichever OS runs it.
	require.NotEmpty(t, symlinkErrnos)
	for _, errno := range symlinkErrnos {
		assert.True(t, isSymlinkErrno(&os.PathError{Op: "open", Path: "/x", Err: errno}),
			"a wrapped errno on the platform list must read as a symbolic link: %v", errno)
	}
	assert.False(t, isSymlinkErrno(&os.PathError{Op: "open", Path: "/x", Err: syscall.EPERM}),
		"an unrelated errno must not read as a symbolic link: %v", syscall.EPERM)
}

func TestLoadConfigRefusesASymlinkToAFileOwnedByAnotherAccount(t *testing.T) {
	setupTestConfig(t)
	if os.Geteuid() == 0 {
		t.Skip("root owns the file this borrows")
	}
	dir := filepath.Join(os.Getenv("HOME"), ConfigFileDir)
	require.NoError(t, os.MkdirAll(dir, 0700))
	// A link is refused a chmod but not a read, so the owner refusal has to run
	// a second time on whatever it resolves to. Without it the one file that
	// names the host and decides whether its certificate is checked can be
	// handed over by anyone who can write in the config directory.
	require.NoError(t, os.Symlink("/etc/hosts", filepath.Join(dir, ConfigFileName)))

	_, err := LoadConfig()
	require.ErrorIs(t, err, errUnsafeConfigFile)
	assert.ErrorContains(t, err, "is owned by uid 0")
}

// narrowConfigFile runs the narrowing for its effect on the mode alone, the way
// a caller that only wants the file protected before reading it its own way
// would. Every caller in production keeps the handle instead.
func narrowConfigFile(path string) error {
	file, err := openNarrowedConfigFile(path)
	if err != nil {
		return err
	}
	return file.Close()
}
