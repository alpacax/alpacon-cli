package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigPermissions(t *testing.T) {
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

func TestConfigDirectoryPermissions(t *testing.T) {
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
