package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigNarrowsWhatIsWideAndKeepsWhatIsStricter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions are not enforced on Windows")
	}
	for _, tc := range []struct {
		name                                 string
		fileMode, dirMode, wantFile, wantDir os.FileMode
	}{
		{"wide", 0644, 0777, 0600, 0700},
		{"executable file", 0755, 0755, 0600, 0700},
		{"strict", 0400, 0500, 0400, 0500},
		{"search only directory", 0400, 0100, 0400, 0100},
		{"wide search only directory", 0400, 0111, 0400, 0100},
		{"wide writable searchable directory", 0600, 0311, 0600, 0300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Root reads a directory with no owner read bit, so the searchable fallback never runs.
			if os.Geteuid() == 0 && tc.dirMode&0400 == 0 {
				t.Skip("root bypasses file permission checks")
			}
			setupTestConfig(t)
			require.NoError(t, saveConfig(&Config{Token: "test-token"}))
			dir := filepath.Join(os.Getenv("HOME"), ConfigFileDir)
			path := filepath.Join(dir, ConfigFileName)
			t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			require.NoError(t, os.Chmod(path, tc.fileMode))
			require.NoError(t, os.Chmod(dir, tc.dirMode))
			cfg, err := LoadConfig()
			require.NoError(t, err)
			assert.Equal(t, "test-token", cfg.Token)
			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, tc.wantFile, info.Mode().Perm())
			info, err = os.Stat(dir)
			require.NoError(t, err)
			assert.Equal(t, tc.wantDir, info.Mode().Perm())
		})
	}
}

func TestConfigOperationsNarrowAWideOpenDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions are not enforced on Windows")
	}
	for _, tc := range []struct {
		name           string
		run            func() error
		existingDevice bool
	}{
		{"save config", func() error { return saveConfig(&Config{}) }, false},
		{"create device", func() error { _, err := GetOrCreateDeviceID(); return err }, false},
		{"read device", func() error { _, err := GetOrCreateDeviceID(); return err }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupTestConfig(t)
			dir := filepath.Join(os.Getenv("HOME"), ConfigFileDir)
			require.NoError(t, os.MkdirAll(dir, 0700))
			if tc.existingDevice {
				require.NoError(t, os.WriteFile(filepath.Join(dir, DeviceIDFileName), []byte("valid-device-id"), 0600))
			}
			require.NoError(t, os.Chmod(dir, 0777))
			require.NoError(t, tc.run())
			info, err := os.Stat(dir)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
		})
	}
}

func TestRestrictFileModeUsesOpenFile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions are not enforced on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
	require.NoError(t, os.Chmod(path, 0644))
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	moved := filepath.Join(dir, "original")
	require.NoError(t, os.Rename(path, moved))
	require.NoError(t, os.WriteFile(path, []byte("replacement"), 0600))
	require.NoError(t, os.Chmod(path, 0644))
	require.NoError(t, restrictFileMode(file, 0600))
	info, err := os.Stat(moved)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	info, err = os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
	require.NoError(t, file.Close())
	assert.Error(t, restrictFileMode(file, 0600))
}

func TestSearchableConfigDirectoryOperations(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires searchable directory handles")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	for _, tc := range []struct {
		name     string
		mode     os.FileMode
		existing bool
		run      func() error
	}{
		{"save config", 0311, false, func() error { return saveConfig(&Config{}) }},
		{"create device", 0311, false, func() error { _, err := GetOrCreateDeviceID(); return err }},
		{"read device", 0111, true, func() error {
			id, err := GetOrCreateDeviceID()
			if err == nil {
				assert.Equal(t, "valid-device-id", id)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupTestConfig(t)
			dir := filepath.Join(os.Getenv("HOME"), ConfigFileDir)
			require.NoError(t, os.Mkdir(dir, 0700))
			t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			if tc.existing {
				require.NoError(t, os.WriteFile(filepath.Join(dir, DeviceIDFileName), []byte("valid-device-id"), 0600))
			}
			require.NoError(t, os.Chmod(dir, tc.mode))
			require.NoError(t, tc.run())
			info, err := os.Stat(dir)
			require.NoError(t, err)
			assert.Equal(t, tc.mode&0700, info.Mode().Perm())
		})
	}
}

// openKeptMode hands back a handle on a file whose mode a chmod will not move,
// which is how a filesystem without permission bits answers one.
func openKeptMode(t *testing.T, mode os.FileMode) (*os.File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
	require.NoError(t, os.Chmod(path, mode))
	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	return file, path
}

func TestRestrictOpenFileModeJudgesAModeAChmodKept(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions are not enforced on Windows")
	}
	// A FAT volume on macOS reports every file as 0700, which exposes nothing.
	// Linux vfat refuses the call instead of keeping the mode, so a refusal is
	// judged the same way: what it left behind is what says whether anything is
	// exposed. That branch decides whether a vfat user gets a device id at all.
	refused := errors.New("operation not permitted")
	for _, tc := range []struct {
		name      string
		mode      os.FileMode
		chmodErr  error
		wantError bool
	}{
		{"the chmod reported success and other accounts keep access", 0644, nil, true},
		{"the chmod reported success and only the owner execute bit survives", 0700, nil, false},
		{"the chmod was refused and other accounts keep access", 0644, refused, true},
		{"the chmod was refused over a mode no other account can use", 0700, refused, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			file, path := openKeptMode(t, tc.mode)
			info, err := file.Stat()
			require.NoError(t, err)
			err = restrictOpenFileMode(file, info, 0600, func(os.FileMode) error { return tc.chmodErr })
			if !tc.wantError {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			if tc.chmodErr != nil {
				// The refusal is handed back as it came, so a caller can still
				// sort on the errno behind it.
				assert.ErrorIs(t, err, tc.chmodErr)
				return
			}
			assert.Equal(t, fmt.Sprintf("%s kept mode %04o after a chmod to 0600", path, tc.mode), err.Error())
		})
	}
}

func TestRestrictConfigDirectoryModeAcceptsADirectoryClosedToEveryone(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	dir := filepath.Join(t.TempDir(), ConfigFileDir)
	require.NoError(t, os.Mkdir(dir, 0700))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	// Mode 0000 hands out nothing, so there is nothing left to narrow. Only
	// darwin can fail this: its search-only open needs an owner execute bit,
	// while linux opens an O_PATH handle whatever the mode says.
	require.NoError(t, os.Chmod(dir, 0000))
	assert.NoError(t, restrictConfigDirectoryMode(dir))
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0000), info.Mode().Perm())
}

func TestWarnUnrestrictedSpeaksOncePerSubjectAndCause(t *testing.T) {
	// Serial: it swaps os.Stderr and clears a package-level map.
	warnedUnrestricted = sync.Map{}
	t.Cleanup(func() { warnedUnrestricted = sync.Map{} })

	readOnly := errors.New("read-only file system")
	foreign := errors.New("owned by another account")
	_, stderr := testutil.CaptureOutput(t, func() {
		warnUnrestricted("config file", readOnly)
		warnUnrestricted("config file", readOnly)
		// A different cause on the same subject is a different condition, and
		// silencing it would hide the one the user can still act on.
		warnUnrestricted("config file", foreign)
		warnUnrestricted("config directory", readOnly)
		warnUnrestricted("config file", nil)
	})

	assert.Equal(t, 1, strings.Count(stderr, "config file permissions: read-only file system"))
	assert.Equal(t, 1, strings.Count(stderr, "config file permissions: owned by another account"))
	assert.Equal(t, 1, strings.Count(stderr, "config directory permissions: read-only file system"))
	// The nil cause is not a failure and must not speak at all.
	assert.Equal(t, 3, strings.Count(stderr, "could not restrict"))
}
