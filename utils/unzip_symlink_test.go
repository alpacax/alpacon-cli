package utils

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnzip_ConfinesSymlinks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		target string
		entry  string
	}{
		{"file", "../outside/file", "link"},
		{"directory", "../outside", "link/file"},
		{"new directory", "../outside", "link/new/"},
		{"new file", "../outside", "link/new/file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			dest := filepath.Join(dir, "dest")
			outside := filepath.Join(dir, "outside")
			require.NoError(t, os.Mkdir(dest, 0755))
			require.NoError(t, os.Mkdir(outside, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(outside, "file"), []byte("original"), 0600))
			makeUnzipSymlink(t, filepath.FromSlash(tt.target), filepath.Join(dest, "link"))
			archive := writeUnzipTestArchive(t, dir, tt.entry)

			require.Error(t, Unzip(archive, dest))
			content, err := os.ReadFile(filepath.Join(outside, "file"))
			require.NoError(t, err)
			assert.Equal(t, "original", string(content))
			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			assert.Len(t, entries, 1)
		})
	}
}

func TestUnzip_AllowsInternalSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	require.NoError(t, os.MkdirAll(filepath.Join(dest, "real"), 0755))
	makeUnzipSymlink(t, "real", filepath.Join(dest, "link"))
	require.NoError(t, Unzip(writeUnzipTestArchive(t, dir, "link/file"), dest))
	content, err := os.ReadFile(filepath.Join(dest, "real", "file"))
	require.NoError(t, err)
	assert.Equal(t, "replacement", string(content))
}

func TestUnzip_MaterializesArchiveSymlinkAsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "test.zip")
	f, err := os.Create(archive)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0777)
	w, err := zw.CreateHeader(header)
	require.NoError(t, err)
	_, err = w.Write([]byte("../outside"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	dest := filepath.Join(dir, "dest")
	require.NoError(t, Unzip(archive, dest))
	info, err := os.Lstat(filepath.Join(dest, "link"))
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), "extracted mode: %v", info.Mode())
	content, err := os.ReadFile(filepath.Join(dest, "link"))
	require.NoError(t, err)
	assert.Equal(t, "../outside", string(content))
}

func makeUnzipSymlink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err != nil && runtime.GOOS == "windows" {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.NoError(t, err)
}

func writeUnzipTestArchive(t *testing.T, dir, entry string) string {
	t.Helper()
	archive := filepath.Join(dir, "test.zip")
	f, err := os.Create(archive)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	w, err := zw.Create(entry)
	require.NoError(t, err)
	if entry[len(entry)-1] != '/' {
		_, err = w.Write([]byte("replacement"))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	return archive
}
