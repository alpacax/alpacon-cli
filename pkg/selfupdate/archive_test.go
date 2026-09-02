package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractBinaryFromTarGz(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-linux-amd64.tar.gz")
	writeTarGz(t, archivePath, map[string]string{
		"LICENSE": "license text",
		"alpacon": "binary content",
	})

	extracted, err := ExtractBinary(archivePath, "alpacon", dir)

	require.NoError(t, err)
	content, readErr := os.ReadFile(extracted)
	require.NoError(t, readErr)
	assert.Equal(t, "binary content", string(content))
}

func TestExtractBinaryFromZip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-windows-amd64.zip")
	writeZip(t, archivePath, map[string]string{
		"README.md":   "readme",
		"alpacon.exe": "windows binary",
	})

	extracted, err := ExtractBinary(archivePath, "alpacon.exe", dir)

	require.NoError(t, err)
	content, readErr := os.ReadFile(extracted)
	require.NoError(t, readErr)
	assert.Equal(t, "windows binary", string(content))
}

func TestExtractBinaryFailsWhenTheEntryIsMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-linux-amd64.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"LICENSE": "license text"})

	_, err := ExtractBinary(archivePath, "alpacon", dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no entry named alpacon")
}

func TestExtractBinaryFailsWhenTheZipEntryIsMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-windows-amd64.zip")
	writeZip(t, archivePath, map[string]string{"LICENSE": "license text"})

	_, err := ExtractBinary(archivePath, "alpacon.exe", dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no entry named alpacon.exe")
}

func writeTarGz(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range entries {
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{
			Name: name, Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}))
		_, writeErr := tarWriter.Write([]byte(content))
		require.NoError(t, writeErr)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	require.NoError(t, file.Close())
}

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	zipWriter := zip.NewWriter(file)
	for name, content := range entries {
		entry, createErr := zipWriter.Create(name)
		require.NoError(t, createErr)
		_, writeErr := entry.Write([]byte(content))
		require.NoError(t, writeErr)
	}
	require.NoError(t, zipWriter.Close())
	require.NoError(t, file.Close())
}

func TestExtractBinaryReturnsNoPathWhenItFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-linux-amd64.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"README": "not the binary"})

	destPath, err := ExtractBinary(archivePath, "alpacon", dir)

	require.Error(t, err)
	assert.Empty(t, destPath, "a path the extraction never wrote must not be handed to the caller")
}

func TestExtractBinarySkipsAZipEntryThatIsNotAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-windows-amd64.zip")
	file, err := os.Create(archivePath)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	_, err = writer.Create("payload/alpacon.exe/")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())

	_, err = ExtractBinary(archivePath, "alpacon.exe", dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no entry named alpacon.exe")
}

func TestExtractBinarySkipsATarEntryThatIsNotAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-linux-amd64.tar.gz")
	file, err := os.Create(archivePath)
	require.NoError(t, err)
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: "payload/alpacon/", Typeflag: tar.TypeDir, Mode: 0755}))
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	require.NoError(t, file.Close())

	_, err = ExtractBinary(archivePath, "alpacon", dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no entry named alpacon")
}

func TestExtractBinaryRefusesAnEntryPastTheSizeBound(t *testing.T) {
	original := maxReleaseFileSize
	t.Cleanup(func() { maxReleaseFileSize = original })
	maxReleaseFileSize = 8

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-linux-amd64.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"alpacon": "far more than eight bytes"})

	_, err := ExtractBinary(archivePath, "alpacon", dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds 8 bytes")
}

func TestExtractBinaryAcceptsAnEntryExactlyAtTheSizeBound(t *testing.T) {
	content := "exactly8"
	original := maxReleaseFileSize
	t.Cleanup(func() { maxReleaseFileSize = original })
	maxReleaseFileSize = int64(len(content))

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-linux-amd64.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"alpacon": content})

	destPath, err := ExtractBinary(archivePath, "alpacon", dir)

	require.NoError(t, err, "the bound is the largest size allowed, not the first refused")
	extracted, readErr := os.ReadFile(destPath)
	require.NoError(t, readErr)
	assert.Equal(t, content, string(extracted))
}

// Structurally impossible today, which is why it is pinned: a refactor that
// starts honoring header.Name would pass every other test in this file.
func TestExtractBinaryNeverWritesOutsideTheDestinationDirectory(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	dir := filepath.Join(parent, "work")
	require.NoError(t, os.Mkdir(dir, 0755))
	archivePath := filepath.Join(dir, "alpacon-1.4.0-linux-amd64.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"../../evil/alpacon": "payload"})

	extracted, err := ExtractBinary(archivePath, "alpacon", dir)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "alpacon.new"), extracted)
	_, statErr := os.Stat(filepath.Join(parent, "evil"))
	assert.ErrorIs(t, statErr, os.ErrNotExist, "an archive must not decide where its entry lands")
}

func TestExtractBinaryRefusesATarStreamThatKeepsInflating(t *testing.T) {
	original := maxArchiveStreamSize
	t.Cleanup(func() { maxArchiveStreamSize = original })
	maxArchiveStreamSize = 64 // Smaller than one tar block, so the scan is cut off before it reaches any header.

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "alpacon-1.4.0-linux-amd64.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"alpacon": "binary content"})

	_, err := ExtractBinary(archivePath, "alpacon", dir)

	require.Error(t, err, "the entry bound caps what is written, never what gzip inflates while scanning")
	assert.NotContains(t, err.Error(), "no entry named", "a truncated scan must not read as an archive missing its binary")
}
