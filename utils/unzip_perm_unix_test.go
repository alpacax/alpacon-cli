//go:build unix

package utils

import (
	"archive/zip"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: syscall.Umask is process-wide, and 0755 is indistinguishable from
// 0777 under the usual 022 umask.
func TestUnzip_DirectoriesUseFixedPerm(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	tmpDir := t.TempDir()
	extractDir := filepath.Join(tmpDir, "extract")
	zipPath := writeUnzipArchive(t, tmpDir, func(zw *zip.Writer) {
		header := &zip.FileHeader{Name: "dir/", Method: zip.Deflate}
		header.SetMode(os.ModeDir | 0o777)
		_, err := zw.CreateHeader(header)
		require.NoError(t, err)

		fw, err := zw.Create("nested/file.txt")
		require.NoError(t, err)
		_, err = fw.Write([]byte("content"))
		require.NoError(t, err)
	})

	require.NoError(t, Unzip(zipPath, extractDir))

	for _, dir := range []string{
		extractDir,
		filepath.Join(extractDir, "dir"),
		filepath.Join(extractDir, "nested"),
	} {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), dir)
	}
}
