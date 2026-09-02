//go:build windows

package selfupdate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplaceBinaryInstallsTheNewBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon.exe")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))
	replacement := filepath.Join(dir, "alpacon.exe.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	require.NoError(t, ReplaceBinary(target, replacement))

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new binary", string(content))
}

func TestReplaceBinaryKeepsThePreservedCopyForTheNextSweep(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon.exe")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))
	replacement := filepath.Join(dir, "alpacon.exe.new")
	require.NoError(t, os.WriteFile(replacement, []byte("new binary"), 0600))

	require.NoError(t, ReplaceBinary(target, replacement))

	assert.Equal(t, 1, SweepPreserved(dir, "alpacon.exe"),
		"the running executable stays locked, so its copy waits for the next update to sweep it—the staging copy was renamed into place and must not be among them")
}

func TestReplaceBinaryRestoresTheOldBinaryWhenTheSourceIsMissing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "alpacon.exe")
	require.NoError(t, os.WriteFile(target, []byte("old binary"), 0755))

	err := ReplaceBinary(target, filepath.Join(dir, "does-not-exist"))

	require.Error(t, err)
	content, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "old binary", string(content), "a copy that never started must leave the install path alone")
	assert.Equal(t, 0, SweepPreserved(dir, "alpacon.exe"), "nothing was moved aside, so nothing may be left behind")
}
