package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreservedNameCarriesATimestamp(t *testing.T) {
	now := time.Unix(0, 1735689600000000000)

	name := PreservedName("/usr/local/bin/alpacon", now)

	assert.Equal(t, "/usr/local/bin/alpacon.old.1735689600000000000", name)
}

func TestStagedNameCarriesATimestamp(t *testing.T) {
	now := time.Unix(0, 1735689600000000000)

	name := StagedName("/usr/local/bin/alpacon", now)

	assert.Equal(t, "/usr/local/bin/alpacon.staged.1735689600000000000", name)
}

func TestRestorePreservedPutsTheBinaryBack(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon")
	preserved := filepath.Join(dir, "alpacon.old.1735689600000000000")
	require.NoError(t, os.WriteFile(preserved, []byte("old binary"), 0755))

	require.NoError(t, RestorePreserved(preserved, target))

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "old binary", string(content))
	_, statErr := os.Stat(preserved)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestSweepPreservedRemovesOnlyTimestampedCopies(t *testing.T) {
	dir := t.TempDir()
	keep := []string{"alpacon", "alpacon.old", "alpacon.old.notanumber", "other.old.123", "alpacon.staged"}
	remove := []string{"alpacon.old.1", "alpacon.old.1735689600000000000", "alpacon.staged.1735689600000000000"}
	for _, name := range append(append([]string{}, keep...), remove...) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600))
	}

	removed := SweepPreserved(dir, "alpacon")

	assert.Equal(t, 3, removed)
	for _, name := range keep {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "sweep must not remove %s", name)
	}
	for _, name := range remove {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.ErrorIs(t, err, os.ErrNotExist, "sweep must remove %s", name)
	}
}

func TestRestorePreservedCopiesWhenTheRenameFails(t *testing.T) {
	original := osRename
	t.Cleanup(func() { osRename = original })
	osRename = func(string, string) error { return errors.New("cross-device link") }

	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon")
	preserved := filepath.Join(dir, "alpacon.old.1735689600000000000")
	require.NoError(t, os.WriteFile(target, []byte("half-written binary"), 0755))
	require.NoError(t, os.WriteFile(preserved, []byte("old binary"), 0755))

	require.NoError(t, RestorePreserved(preserved, target))

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "old binary", string(content))
	_, statErr := os.Stat(preserved)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestCopyFileRefusesToWriteOverAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(source, []byte("new binary"), 0755))
	dest := filepath.Join(dir, "alpacon.old.1735689600000000000")
	require.NoError(t, os.WriteFile(dest, []byte("someone else's file"), 0600))

	err := copyFile(source, dest)

	require.ErrorIs(t, err, os.ErrExist, "every destination carries a fresh timestamp, so a file already there is not ours")
	content, readErr := os.ReadFile(dest)
	require.NoError(t, readErr)
	assert.Equal(t, "someone else's file", string(content))
}
