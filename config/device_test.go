package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readDeviceIDFile returns the raw contents of the device identifier file.
func readDeviceIDFile(t *testing.T) string {
	t.Helper()
	path, err := deviceIDPath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// writeDeviceIDFile plants raw contents so tests can exercise values the writer
// would never produce.
func writeDeviceIDFile(t *testing.T, raw string) {
	t.Helper()
	path, err := deviceIDPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(raw), 0600))
}

func TestIsValidDeviceID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"generated UUID", "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b", true},
		{"minimum length", "abcd1234", true},
		{"maximum length", strings.Repeat("a", 64), true},
		{"empty", "", false},
		{"too short", "abc1234", false},
		{"too long", strings.Repeat("a", 65), false},
		{"underscore", "abcd_1234", false},
		{"colon", "device:1234", false},
		{"whitespace", "abcd 1234", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsValidDeviceID(tt.id))
		})
	}
}

func TestNewDeviceID_IsUniqueAndAcceptedByAuth0Pattern(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := newDeviceID()
		require.NoError(t, err)
		assert.True(t, IsValidDeviceID(id), "generated id must satisfy the Auth0 action pattern: %q", id)
		assert.False(t, seen[id], "generated id must not repeat: %q", id)
		seen[id] = true
	}
}

func TestGetOrCreateDeviceID_GeneratesOnceAndPersists(t *testing.T) {
	setupTestConfig(t)

	first, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	assert.True(t, IsValidDeviceID(first), "device id must satisfy the Auth0 action pattern: %q", first)

	second, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	assert.Equal(t, first, second)

	assert.Equal(t, first+"\n", readDeviceIDFile(t), "file holds one line of plain text")
}

func TestGetOrCreateDeviceID_LivesBesideConfigNotInIt(t *testing.T) {
	setupTestConfig(t)

	deviceID, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	require.NoError(t, CreateConfig(
		"https://ws.us1.alpacon.io", "ws",
		"", "", "access-token", "refresh-token",
		"alpacon.io", 3600, false,
	))

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	configData, err := os.ReadFile(filepath.Join(homeDir, ConfigFileDir, ConfigFileName))
	require.NoError(t, err)
	assert.NotContains(t, string(configData), deviceID, "the identifier must not be written into config.json")
	assert.NotContains(t, string(configData), "device_id")
}

// TestGetOrCreateDeviceID_SurvivesLogout is the property the separate file
// exists to buy. DeleteConfig removes config.json; if it took the identifier
// with it, the next login would mint a new one and orphan every presence proof
// bound to the old one, re-challenging the user on their first privileged
// action. The machine did not change, so the identifier must not either.
func TestGetOrCreateDeviceID_SurvivesLogout(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig(
		"https://ws.us1.alpacon.io", "ws",
		"", "", "access-token", "refresh-token",
		"alpacon.io", 3600, false,
	))
	deviceID, err := GetOrCreateDeviceID()
	require.NoError(t, err)

	require.NoError(t, DeleteConfig())

	_, err = LoadConfig()
	require.Error(t, err, "logout must have removed the config file")

	afterLogout, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	assert.Equal(t, deviceID, afterLogout)

	// And the re-login that follows must not disturb it either.
	require.NoError(t, CreateConfig(
		"https://ws.us1.alpacon.io", "ws",
		"", "", "new-access-token", "new-refresh-token",
		"alpacon.io", 3600, false,
	))
	afterReLogin, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	assert.Equal(t, deviceID, afterReLogin)
}

func TestGetOrCreateDeviceID_SurvivesConfigWrites(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig(
		"https://ws1.us1.alpacon.io", "ws1",
		"", "", "access-token", "refresh-token",
		"alpacon.io", 3600, false,
	))
	deviceID, err := GetOrCreateDeviceID()
	require.NoError(t, err)

	writers := []struct {
		name  string
		write func(t *testing.T)
	}{
		{"workspace switch", func(t *testing.T) {
			require.NoError(t, SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2"))
		}},
		{"access token refresh", func(t *testing.T) {
			require.NoError(t, SaveRefreshedAuth0Token("refreshed-access-token", 3600))
		}},
		{"active work session", func(t *testing.T) {
			require.NoError(t, SetActiveWorkSession("6f1c1d0e-0000-0000-0000-000000000000"))
		}},
	}
	for _, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			w.write(t)
			got, err := GetOrCreateDeviceID()
			require.NoError(t, err)
			assert.Equal(t, deviceID, got)
		})
	}
}

func TestGetOrCreateDeviceID_ReplacesMalformedValue(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty file", ""},
		{"whitespace only", "   \n"},
		{"too short", "short\n"},
		{"illegal character", "not_a_valid_id\n"},
		{"too long", strings.Repeat("a", 65) + "\n"},
		{"two lines", "abcd1234\nabcd5678\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			writeDeviceIDFile(t, tt.raw)

			deviceID, err := GetOrCreateDeviceID()
			require.NoError(t, err)
			assert.True(t, IsValidDeviceID(deviceID), "replacement id must satisfy the Auth0 action pattern: %q", deviceID)
			assert.Equal(t, deviceID+"\n", readDeviceIDFile(t))
		})
	}
}

// TestGetOrCreateDeviceID_TolerantOfSurroundingWhitespace keeps a hand-edited
// or editor-rewritten file working instead of silently regenerating.
func TestGetOrCreateDeviceID_TolerantOfSurroundingWhitespace(t *testing.T) {
	setupTestConfig(t)
	writeDeviceIDFile(t, "  0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b \r\n")

	deviceID, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	assert.Equal(t, "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b", deviceID)
}

func TestGetOrCreateDeviceID_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not meaningful on Windows")
	}
	setupTestConfig(t)

	_, err := GetOrCreateDeviceID()
	require.NoError(t, err)

	path, err := deviceIDPath()
	require.NoError(t, err)
	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm())

	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())
}

// TestGetOrCreateDeviceID_UnreadableFile pins that a read failure is surfaced
// rather than papered over with a fresh identifier: silently regenerating would
// hide a permissions problem and change the identifier on every invocation.
func TestGetOrCreateDeviceID_UnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not meaningful on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	setupTestConfig(t)
	writeDeviceIDFile(t, "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b\n")

	path, err := deviceIDPath()
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0000))
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })

	deviceID, err := GetOrCreateDeviceID()
	assert.Error(t, err)
	assert.Empty(t, deviceID)
}
