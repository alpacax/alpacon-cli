//go:build !windows

package selfupdate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A trimmed mode on the preserved copy re-permissions the install the moment a
// rollback renames it back.
func TestCopyFileKeepsTheSourceModeThroughTheUmask(t *testing.T) {
	original := syscall.Umask(0022)
	t.Cleanup(func() { syscall.Umask(original) })

	dir := t.TempDir()
	source := filepath.Join(dir, "alpacon")
	require.NoError(t, os.WriteFile(source, []byte("old binary"), 0600))
	require.NoError(t, os.Chmod(source, 0775))
	dest := filepath.Join(dir, "alpacon.old.1735689600000000000")

	require.NoError(t, copyFile(source, dest))

	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0775), info.Mode().Perm())
}
