package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustDeviceIDPath returns the device identifier file path or fails the test.
func mustDeviceIDPath(t *testing.T) string {
	t.Helper()
	path, err := deviceIDPath()
	require.NoError(t, err)
	return path
}

// readDeviceIDFile returns the raw contents of the device identifier file.
func readDeviceIDFile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(mustDeviceIDPath(t))
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
	// Assert nothing is readable by group or other, rather than an exact mode:
	// creation respects the process umask, so a stricter umask can legally
	// produce 0400 here and an exact comparison would fail on that machine.
	assert.Zero(t, fileInfo.Mode().Perm()&0o077,
		"device id file must not be readable by group or other, got %v", fileInfo.Mode().Perm())

	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Zero(t, dirInfo.Mode().Perm()&0o077,
		"config dir must not be accessible by group or other, got %v", dirInfo.Mode().Perm())
}

// TestGetOrCreateDeviceID_ConcurrentCreation is the property exclusive creation
// buys. Two `alpacon` invocations can start at the same moment—a shell script
// backgrounding several commands is enough—and read-then-write lets both
// generate and both store. The loser would authenticate under an identifier its
// own installation never sends again, orphaning that login's presence proof, so
// every caller must come away with the one value the file ends up holding.
func TestGetOrCreateDeviceID_ConcurrentCreation(t *testing.T) {
	setupTestConfig(t)

	const callers = 32
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(callers)

	results := make([]string, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer done.Done()
			start.Wait()
			results[i], errs[i] = GetOrCreateDeviceID()
		}(i)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		require.NoError(t, err, "caller %d", i)
	}
	stored := strings.TrimSpace(readDeviceIDFile(t))
	require.True(t, IsValidDeviceID(stored), "stored id must satisfy the Auth0 action pattern: %q", stored)
	for i, deviceID := range results {
		assert.Equal(t, stored, deviceID, "caller %d must return the identifier the file ended up holding", i)
	}

	// A concurrent loser must not leave its scratch file behind either.
	entries, err := os.ReadDir(filepath.Dir(mustDeviceIDPath(t)))
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), DeviceIDFileName+"-"),
			"temporary file %q was left behind", entry.Name())
	}
}

// TestGetOrCreateDeviceID_ConcurrentReplacementOfMalformedValue covers the same
// race one step later: the file exists but holds a value the Auth0 action would
// reject, so every caller has to replace it.
//
// This asserts less than the creation case above, and deliberately. Replacing a
// path cannot be made exclusive without a lock, so concurrent replacers can
// overwrite one another and a caller can return an identifier a later replacer
// has already superseded. What must hold is that nobody is handed something
// unusable and that the file is left in a state the next run can read: every
// caller gets a value the Auth0 action accepts, and the value left on disk is
// one of the values that were handed out rather than a torn or empty file.
func TestGetOrCreateDeviceID_ConcurrentReplacementOfMalformedValue(t *testing.T) {
	setupTestConfig(t)
	writeDeviceIDFile(t, "not_a_valid_id\n")

	const callers = 16
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(callers)

	results := make([]string, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer done.Done()
			start.Wait()
			results[i], errs[i] = GetOrCreateDeviceID()
		}(i)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		require.NoError(t, err, "caller %d", i)
	}
	stored := strings.TrimSpace(readDeviceIDFile(t))
	require.True(t, IsValidDeviceID(stored), "stored id must satisfy the Auth0 action pattern: %q", stored)
	handedOut := false
	for i, deviceID := range results {
		assert.True(t, IsValidDeviceID(deviceID),
			"caller %d must get a value the Auth0 action accepts, got %q", i, deviceID)
		if deviceID == stored {
			handedOut = true
		}
	}
	assert.True(t, handedOut, "the value left on disk must be one that was handed out, got %q", stored)
}

// TestGetOrCreateDeviceID_TightensAnExistingPermissiveFile pins that the mode is
// what this package claims it is. os.WriteFile leaves the mode of an existing
// file alone, so an identifier file created world-readable by some other path
// used to keep that mode for the life of the installation.
func TestGetOrCreateDeviceID_TightensAnExistingPermissiveFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not meaningful on Windows")
	}

	tests := []struct {
		name string
		raw  string
		mode os.FileMode
	}{
		{"well-formed value is kept, mode is tightened", "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b\n", 0644},
		{"malformed value is replaced, mode is tightened", "not_a_valid_id\n", 0666},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			writeDeviceIDFile(t, tt.raw)
			path := mustDeviceIDPath(t)
			require.NoError(t, os.Chmod(path, tt.mode))

			deviceID, err := GetOrCreateDeviceID()
			require.NoError(t, err)
			assert.True(t, IsValidDeviceID(deviceID))

			fileInfo, err := os.Stat(path)
			require.NoError(t, err)
			assert.Zero(t, fileInfo.Mode().Perm()&0o077,
				"device id file must not be readable by group or other, got %v", fileInfo.Mode().Perm())
		})
	}
}

// TestGetOrCreateDeviceID_KeepsAStricterMode pins that tightening only ever
// removes bits. A user whose umask produces a read-only file meant that, and
// widening it back to 0600 would be this package undoing the protection it
// claims to provide.
func TestGetOrCreateDeviceID_KeepsAStricterMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not meaningful on Windows")
	}
	setupTestConfig(t)
	writeDeviceIDFile(t, "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b\n")
	path := mustDeviceIDPath(t)
	require.NoError(t, os.Chmod(path, 0400))
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })

	_, err := GetOrCreateDeviceID()
	require.NoError(t, err)

	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0400), fileInfo.Mode().Perm())
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
