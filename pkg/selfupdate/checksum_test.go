package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseChecksums(t *testing.T) {
	data := []byte("" +
		"9f2d1c0e6b3a4d5f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7  alpacon-1.4.0-linux-amd64.tar.gz\n" +
		"1122334455667788990011223344556677889900112233445566778899001122  alpacon-1.4.0-windows-amd64.zip\n" +
		"\n")

	sums := ParseChecksums(data)

	assert.Len(t, sums, 2)
	assert.Equal(t, "9f2d1c0e6b3a4d5f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7", sums["alpacon-1.4.0-linux-amd64.tar.gz"])
	assert.Equal(t, "1122334455667788990011223344556677889900112233445566778899001122", sums["alpacon-1.4.0-windows-amd64.zip"])
}

func TestVerifyChecksumAcceptsAMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	content := []byte("archive content")
	require.NoError(t, os.WriteFile(path, content, 0600))
	sum := sha256.Sum256(content)

	assert.NoError(t, VerifyChecksum(path, hex.EncodeToString(sum[:])))
}

func TestVerifyChecksumRejectsAMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	require.NoError(t, os.WriteFile(path, []byte("archive content"), 0600))

	err := VerifyChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000")

	assert.ErrorIs(t, err, ErrChecksumMismatch)
}

func TestVerifyChecksumRejectsAnEmptyExpectation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	require.NoError(t, os.WriteFile(path, []byte("archive content"), 0600))

	err := VerifyChecksum(path, "")

	assert.ErrorIs(t, err, ErrChecksumMismatch, "a checksums file that never named this archive proves nothing about it")
}

func TestFetchVerifiedBinaryRejectsAnArchiveTheChecksumsNeverName(t *testing.T) {
	release := &Release{Version: "1.4.0"}
	archiveName := ArchiveName("1.4.0", "linux", "amd64")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.sha256") {
			_, _ = w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  alpacon-1.4.0-darwin-arm64.tar.gz\n"))
			return
		}
		_, _ = w.Write([]byte("archive content"))
	}))
	defer server.Close()
	release.Assets = []Asset{
		{Name: archiveName, DownloadURL: server.URL + "/" + archiveName},
		{Name: ChecksumsName("1.4.0"), DownloadURL: server.URL + "/" + ChecksumsName("1.4.0")},
	}

	_, err := FetchVerifiedBinary(release, "linux", "amd64", "alpacon", t.TempDir())

	assert.ErrorIs(t, err, ErrChecksumMismatch)
}

func TestVerifyChecksumStopsBeforeItEvenOpensTheFile(t *testing.T) {
	err := VerifyChecksum(filepath.Join(t.TempDir(), "never-downloaded.tar.gz"), "")

	assert.ErrorIs(t, err, ErrChecksumMismatch)
	assert.NotErrorIs(t, err, fs.ErrNotExist)
}
