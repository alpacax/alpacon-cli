//go:build !windows

package selfupdate

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplaceBinaryKeepsTheDestinationMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0700)) // 0700, not the 0755 the implementation passes: a fixture at 0755 cannot tell keeping the destination's mode from forcing that literal.
	replacement := filepath.Join(dir, "alpacon.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	require.NoError(t, ReplaceBinary(target, replacement))

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new binary", string(content))

	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

func TestReplaceBinaryWarnsWhenAnyLocalAccountCanRewriteTheInstall(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0777))
	require.NoError(t, os.Chmod(target, 0777)) // A umask that narrows WriteFile's 0777 would retire the branch rather than test it.
	replacement := filepath.Join(dir, "alpacon.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	_, stderr := testutil.CaptureOutput(t, func() {
		require.NoError(t, ReplaceBinary(target, replacement))
	})

	assert.Contains(t, stderr, "writable by group or other (0777)")
	assert.Contains(t, stderr, "chmod go-w")
	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0777), info.Mode().Perm(), "the warning replaces a chmod; it must not become one")
}

func TestReplaceBinaryStaysQuietOnAnOrdinaryInstallMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))
	replacement := filepath.Join(dir, "alpacon.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	_, stderr := testutil.CaptureOutput(t, func() {
		require.NoError(t, ReplaceBinary(target, replacement))
	})

	assert.Empty(t, stderr, "0755 is what a normal install looks like, and a warning on every update teaches operators to ignore it")
}

func TestReplaceBinaryLeavesNoPreservedCopyOnSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))
	replacement := filepath.Join(dir, "alpacon.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	require.NoError(t, ReplaceBinary(target, replacement))

	assert.Equal(t, 0, SweepPreserved(dir, "alpacon"), "a successful replace must leave no preserved copy behind")
}

func TestReplaceBinaryRestoresTheOldBinaryWhenTheSourceIsMissing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))

	err := ReplaceBinary(target, filepath.Join(dir, "does-not-exist"))

	require.Error(t, err)
	content, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "old binary", string(content))
	assert.Equal(t, 0, SweepPreserved(dir, "alpacon"), "giving up before the write must not leave a copy behind either")
}

func TestReplaceBinaryRollsBackWhenTheWriteIsRefused(t *testing.T) {
	// Today's SaveStreamAtomic cannot damage the target—its last act is the
	// rename—but the contract ReplaceBinary owes is that a reported failure
	// leaves the old binary installed, and only the rollback can keep it.
	original := writeReplacement
	t.Cleanup(func() { writeReplacement = original })
	writeReplacement = func(name string, _ io.Reader, perm os.FileMode) (int64, error) {
		require.NoError(t, os.WriteFile(name, []byte("half-written binary"), perm))
		return 0, os.ErrPermission
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))
	replacement := filepath.Join(dir, "alpacon.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	err := ReplaceBinary(target, replacement)

	require.ErrorIs(t, err, os.ErrPermission, "the write's own error must survive the rollback beside it")
	content, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "old binary", string(content))
	assert.Equal(t, 0, SweepPreserved(dir, "alpacon"), "a rolled-back replace must leave no preserved copy behind")
}

// The install path is resolved through EvalSymlinks before the download; a link
// planted in the gap would send a root write wherever it points.
func TestReplaceBinaryRefusesATargetThatBecameASymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("someone else's file"), 0600))
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.Symlink(victim, target))
	replacement := filepath.Join(dir, "alpacon.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	original := writeReplacement
	t.Cleanup(func() { writeReplacement = original })
	writeReplacement = func(string, io.Reader, os.FileMode) (int64, error) {
		t.Error("the write must not run against a symlinked target")
		return 0, nil
	}

	err := ReplaceBinary(target, replacement)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
	content, readErr := os.ReadFile(victim)
	require.NoError(t, readErr)
	assert.Equal(t, "someone else's file", string(content))
}

// The file bits are clean here, and the binary is still swappable: replacing a
// file is a rename, which needs permission on the directory and none on the file.
func TestReplaceBinaryWarnsWhenTheInstallDirectoryIsTheWritableOne(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0775))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))
	replacement := filepath.Join(dir, "alpacon.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	_, stderr := testutil.CaptureOutput(t, func() {
		require.NoError(t, ReplaceBinary(target, replacement))
	})

	assert.Contains(t, stderr, dir)
	assert.Contains(t, stderr, "needs no permission on the file itself")
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0775), info.Mode().Perm(), "the warning replaces a chmod; it must not become one")
}

func TestReplaceBinaryStaysQuietOnAStickyWritableDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, os.ModeSticky|0777))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	target := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))
	replacement := filepath.Join(dir, "alpacon.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	_, stderr := testutil.CaptureOutput(t, func() {
		require.NoError(t, ReplaceBinary(target, replacement))
	})

	assert.Empty(t, stderr, "sticky stops a non-owner renaming over somebody else's file, so there is nothing to warn about")
}
