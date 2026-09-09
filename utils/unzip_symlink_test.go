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

func TestUnzip_AllowsAbsoluteInternalSymlinks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, target, entry, want string
		directory                 bool
	}{
		{"directory", "real", "link/new/file", "real/new/file", false},
		{"directory entry", "real", "link/new/", "real/new", true},
		{"existing file", "real/file", "link", "real/file", false},
		{"dangling file", "real/missing", "link", "real/missing", false},
		{"dangling directory", "missing", "link/new/file", "missing/new/file", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			dest := filepath.Join(dir, "dest")
			require.NoError(t, os.MkdirAll(filepath.Join(dest, "real"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(dest, "real", "file"), []byte("original"), 0600))
			target := filepath.Join(dest, filepath.FromSlash(tc.target))
			makeUnzipSymlink(t, target, filepath.Join(dest, "link"))
			require.NoError(t, Unzip(writeUnzipTestArchive(t, dir, tc.entry), dest))
			path := filepath.Join(dest, filepath.FromSlash(tc.want))
			if tc.directory {
				info, err := os.Stat(path)
				require.NoError(t, err)
				assert.True(t, info.IsDir(), "mode: %v", info.Mode())
			} else {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, "replacement", string(data))
			}
			got, err := os.Readlink(filepath.Join(dest, "link"))
			require.NoError(t, err)
			assert.Equal(t, target, got)
		})
	}
}

func TestUnzip_ResolvesLinkBeforeParentComponent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	require.NoError(t, os.MkdirAll(filepath.Join(dest, "real", "subdir"), 0755))
	makeUnzipSymlink(t, filepath.Join("real", "subdir"), filepath.Join(dest, "alias"))
	target := dest + string(os.PathSeparator) + "alias" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "file"
	makeUnzipSymlink(t, target, filepath.Join(dest, "link"))
	require.NoError(t, Unzip(writeUnzipTestArchive(t, dir, "link"), dest))
	data, err := os.ReadFile(filepath.Join(dest, "real", "file"))
	require.NoError(t, err)
	assert.Equal(t, "replacement", string(data))
	_, err = os.Stat(filepath.Join(dest, "file"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestUnzip_AbsoluteLinksWithDestinationAlias(t *testing.T) {
	t.Parallel()
	for _, canonical := range []bool{false, true} {
		name := "original spelling"
		if canonical {
			name = "canonical spelling"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			real := filepath.Join(dir, "real")
			dest := filepath.Join(dir, "dest")
			require.NoError(t, os.MkdirAll(filepath.Join(real, "files"), 0755))
			makeUnzipSymlink(t, real, dest)
			target := filepath.Join(dest, "files")
			if canonical {
				var err error
				target, err = filepath.EvalSymlinks(target)
				require.NoError(t, err)
			}
			makeUnzipSymlink(t, target, filepath.Join(real, "link"))
			require.NoError(t, Unzip(writeUnzipTestArchive(t, dir, "link/file"), dest))
			data, err := os.ReadFile(filepath.Join(real, "files", "file"))
			require.NoError(t, err)
			assert.Equal(t, "replacement", string(data))
		})
	}
}

func TestUnzip_RejectsAbsoluteEscapesAndCycles(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"external file", "external directory", "sibling prefix", "nested escape", "cycle", "parent escape"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			dest := filepath.Join(dir, "dest")
			outside := filepath.Join(dir, "dest-other")
			require.NoError(t, os.Mkdir(dest, 0755))
			require.NoError(t, os.Mkdir(outside, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(outside, "file"), []byte("original"), 0600))
			entry := "link/file"
			target := outside
			switch name {
			case "external file":
				target = filepath.Join(outside, "file")
				entry = "link"
			case "external directory":
				target = dir
				entry = "link/dest-other/file"
			case "nested escape":
				makeUnzipSymlink(t, outside, filepath.Join(dest, "second"))
				target = filepath.Join(dest, "second")
			case "cycle":
				target = filepath.Join(dest, "link")
			case "parent escape":
				target = dest + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "dest-other"
			}
			makeUnzipSymlink(t, target, filepath.Join(dest, "link"))
			require.Error(t, Unzip(writeUnzipTestArchive(t, dir, entry), dest))
			data, err := os.ReadFile(filepath.Join(outside, "file"))
			require.NoError(t, err)
			assert.Equal(t, "original", string(data))
			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			assert.Len(t, entries, 1)
		})
	}
}

func TestUnzip_RejectsParentAfterRegularFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	require.NoError(t, os.Mkdir(dest, 0755))
	for _, name := range []string{"plain", "victim"} {
		require.NoError(t, os.WriteFile(filepath.Join(dest, name), []byte("original"), 0600))
	}
	target := dest + string(os.PathSeparator) + "plain" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "victim"
	makeUnzipSymlink(t, target, filepath.Join(dest, "link"))
	require.Error(t, Unzip(writeUnzipTestArchive(t, dir, "link"), dest))
	data, err := os.ReadFile(filepath.Join(dest, "victim"))
	require.NoError(t, err)
	assert.Equal(t, "original", string(data))
}
