//go:build unix

package utils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: syscall.Umask is process-wide, and 0666 is indistinguishable from
// 0644 under the usual 022 umask.
func TestSaveFile_AppliesUmaskToNewFile(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	dest := filepath.Join(t.TempDir(), "file.txt")

	err := SaveFile(dest, []byte("created"))
	require.NoError(t, err)

	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0666), info.Mode().Perm())
}
