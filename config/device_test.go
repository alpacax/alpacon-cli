package config

import (
	"io/fs"
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

// assertNoLeftoverTempFiles fails when a scratch file survived in the config
// directory. Publishing and replacing both go through one, and either leaks it
// on a path that returns before its cleanup runs.
func assertNoLeftoverTempFiles(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(mustDeviceIDPath(t)))
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), DeviceIDFileName+"-"),
			"temporary file %q was left behind", entry.Name())
	}
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
	t.Parallel()
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
	t.Parallel()
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
			assertNoLeftoverTempFiles(t)
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

// TestCreateDeviceIDFile_ReportsErrExistWhenPathIsTaken pins the contract
// createDeviceID branches on. The concurrent test below reaches it only by
// winning a race, so it can pass on a run where nothing collided; a publication
// step that stopped reporting fs.ErrExist would turn that branch into dead code.
func TestCreateDeviceIDFile_ReportsErrExistWhenPathIsTaken(t *testing.T) {
	setupTestConfig(t)
	path := mustDeviceIDPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	const winner = "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b"
	require.NoError(t, createDeviceIDFile(path, winner))

	err := createDeviceIDFile(path, "1a2b3c4d-5e6f-4a1e-8f2b-1c2d3e4f5a6b")

	require.ErrorIs(t, err, fs.ErrExist)
	assert.Equal(t, winner+"\n", readDeviceIDFile(t), "the loser must not have touched the published value")
}

// TestCreateDeviceIDFileWithoutLink_ReportsErrExistWhenPathIsTaken keeps the
// no-hard-link fallback on the contract createDeviceID branches on. It gives up
// atomicity; it must not give up the exclusion.
func TestCreateDeviceIDFileWithoutLink_ReportsErrExistWhenPathIsTaken(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), DeviceIDFileName)
	const winner = "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b"
	require.NoError(t, createDeviceIDFileWithoutLink(path, winner))

	err := createDeviceIDFileWithoutLink(path, "1a2b3c4d-5e6f-4a1e-8f2b-1c2d3e4f5a6b")

	require.ErrorIs(t, err, fs.ErrExist)
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, winner+"\n", string(data), "the loser must not have touched the published value")
}

// TestCreateDeviceIDFile_NeverPublishesAnEmptyFile is the property #361 was
// about and the one the test above cannot reach: O_EXCL plus a write on the next
// line passes that one too, while the path exists holding nothing. The catch is
// probabilistic—the window is one write wide—but one-sided: a publication step
// with no window cannot fail it, so a failure here is real.
// Serial: the reader below spins without sleeping, and the window it watches is
// one write wide. Sharing a core with parallel neighbours only narrows it.
func TestCreateDeviceIDFile_NeverPublishesAnEmptyFile(t *testing.T) {
	const rounds = 200
	const deviceID = "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b"

	for round := range rounds {
		path := filepath.Join(t.TempDir(), DeviceIDFileName)

		stop := make(chan struct{})
		seen := make(chan string, 1)
		var watching sync.WaitGroup
		watching.Add(1)
		go func() {
			defer watching.Done()
			for {
				data, err := os.ReadFile(path)
				if err == nil {
					if string(data) != deviceID+"\n" {
						seen <- string(data)
					}
					return
				}
				select {
				case <-stop:
					return
				default:
				}
			}
		}()

		err := createDeviceIDFile(path, deviceID)
		close(stop)
		watching.Wait()

		require.NoError(t, err, "round %d", round)
		select {
		case raw := <-seen:
			t.Fatalf("round %d: %s was readable holding %q before it held the identifier",
				round, DeviceIDFileName, raw)
		default:
		}
	}
}

// TestGetOrCreateDeviceID_ConcurrentCreation is the property exclusive creation
// buys. Two `alpacon` invocations can start at once—backgrounding a few shell
// commands does it—and read-then-write lets both generate and both store,
// leaving the loser on an identifier its installation never sends again. Every
// caller must come away with the value the file ends up holding, and exclusivity
// on the name alone does not get there—see createDeviceIDFile.
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
	assertNoLeftoverTempFiles(t)
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
	assertNoLeftoverTempFiles(t)
}

// TestGetOrCreateDeviceID_TightensAnExistingPermissiveFile pins that the mode is
// what this package claims it is. os.WriteFile leaves the mode of an existing
// file alone, so an identifier file created world-readable by some other path
// used to keep that mode for the life of the installation.
//
// The bound asserted is 0600, the one the package documents, rather than the
// group and other bits alone: an owner execute bit says this file is a program
// to run, which it is not, and a mask that spared it would leave the documented
// mode and the enforced one disagreeing.
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
		{"owner execute bit is dropped too", "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b\n", 0700},
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
			assert.Zero(t, fileInfo.Mode().Perm()&^os.FileMode(0o600),
				"device id file must be no more permissive than 0600, got %v", fileInfo.Mode().Perm())
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

// TestGetOrCreateDeviceID_KeepsAStricterModeWhenReplacing carries the property
// above onto the path that rewrites the file. Replacing publishes a fresh
// temporary file, which arrives with the mode os.CreateTemp chose rather than
// the one the file it replaces had, so a read-only identifier file used to come
// back writable—the package widening a mode it promises only ever to narrow.
func TestGetOrCreateDeviceID_KeepsAStricterModeWhenReplacing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not meaningful on Windows")
	}
	setupTestConfig(t)
	writeDeviceIDFile(t, "not_a_valid_id\n")
	path := mustDeviceIDPath(t)
	require.NoError(t, os.Chmod(path, 0400))
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })

	deviceID, err := GetOrCreateDeviceID()
	require.NoError(t, err)
	assert.True(t, IsValidDeviceID(deviceID),
		"replacement id must satisfy the Auth0 action pattern: %q", deviceID)

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

// TestGetOrCreateDeviceID_NamesTheScratchFileInItsError keeps two failures
// apart. One temporary file serves both the path that creates the identifier
// file and the path that rewrites it, so a scratch file that could not be
// opened used to be reported as the identifier file itself—telling a user whose
// device_id is sitting right there that it could not be created.
func TestGetOrCreateDeviceID_NamesTheScratchFileInItsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not meaningful on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	setupTestConfig(t)
	writeDeviceIDFile(t, "not_a_valid_id\n")
	// Readable and traversable, but nothing new can be created in it.
	dir := filepath.Dir(mustDeviceIDPath(t))
	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	deviceID, err := GetOrCreateDeviceID()

	require.Error(t, err)
	assert.Empty(t, deviceID)
	assert.Contains(t, err.Error(), "temporary device id file")
}

// TestWriteDeviceIDTempFile_ReportsAFailureAfterTheScratchFileOpened covers the
// other error the scratch file can produce: it opened, and something between
// there and the caller receiving it failed. The mode narrowing is the reachable
// one—a symlink loop is neither a file it can read nor an absent path—and the
// write, the chmod, and the close report through the same message.
func TestWriteDeviceIDTempFile_ReportsAFailureAfterTheScratchFileOpened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("narrowToPublishedFileMode does nothing on Windows")
	}
	setupTestConfig(t)
	path := mustDeviceIDPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.Symlink(path, path))

	tempPath, err := writeDeviceIDTempFile(path, "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b")

	require.Error(t, err)
	assert.Empty(t, tempPath)
	assert.Contains(t, err.Error(), "failed to prepare temporary device id file")
	assertNoLeftoverTempFiles(t)
}
