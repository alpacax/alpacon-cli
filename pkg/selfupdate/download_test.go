package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allowAssetsFrom points the origin pin at a test server, which it would
// otherwise refuse.
func allowAssetsFrom(t *testing.T, rawURL string) {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	original := allowedAssetOrigins
	t.Cleanup(func() { allowedAssetOrigins = original })
	allowedAssetOrigins = []string{assetOrigin(parsed)}
}

func releaseServer(t *testing.T, dir string, corrupt bool) *Release {
	t.Helper()

	archivePath := filepath.Join(dir, "source.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"alpacon": "new binary"})
	archive, err := os.ReadFile(archivePath)
	require.NoError(t, err)

	sum := sha256.Sum256(archive)
	stated := hex.EncodeToString(sum[:])
	if corrupt {
		stated = "0000000000000000000000000000000000000000000000000000000000000000"
	}
	checksums := fmt.Sprintf("%s  %s\n", stated, ArchiveName("1.4.0", "linux", "amd64"))

	mux := http.NewServeMux()
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(checksums)) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	allowAssetsFrom(t, ts.URL)

	return &Release{
		Version: "1.4.0",
		Assets: []Asset{
			{Name: ArchiveName("1.4.0", "linux", "amd64"), DownloadURL: ts.URL + "/archive"},
			{Name: ChecksumsName("1.4.0"), DownloadURL: ts.URL + "/checksums"},
		},
	}
}

func TestFetchVerifiedBinaryExtractsAVerifiedArchive(t *testing.T) {
	dir := t.TempDir()
	release := releaseServer(t, dir, false)

	path, err := FetchVerifiedBinary(release, "linux", "amd64", "alpacon", dir)

	require.NoError(t, err)
	content, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "new binary", string(content))
}

func TestFetchVerifiedBinaryRefusesAMismatchedChecksum(t *testing.T) {
	dir := t.TempDir()
	release := releaseServer(t, dir, true)

	_, err := FetchVerifiedBinary(release, "linux", "amd64", "alpacon", dir)

	assert.ErrorIs(t, err, ErrChecksumMismatch)
	_, statErr := os.Stat(filepath.Join(dir, "alpacon.new"))
	assert.ErrorIs(t, statErr, os.ErrNotExist, "verification runs before extraction, and only the missing file proves that order")
}

func TestFetchVerifiedBinaryFailsWhenThePlatformHasNoAsset(t *testing.T) {
	dir := t.TempDir()
	release := releaseServer(t, dir, false)

	_, err := FetchVerifiedBinary(release, "windows", "arm64", "alpacon.exe", dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "alpacon-1.4.0-windows-arm64.zip")
}

func TestDownloadToRefusesAResponseThatRunsPastTheLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 64))
	}))
	defer ts.Close()
	allowAssetsFrom(t, ts.URL)
	destPath := filepath.Join(t.TempDir(), "archive.tar.gz")

	err := downloadTo(ts.URL, destPath, 16)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeded 16 bytes")
}

func TestRefuseSchemeDowngrade(t *testing.T) {
	t.Parallel()
	secure := httptest.NewRequest(http.MethodGet, "https://example.test/archive.tar.gz", nil)
	plain := httptest.NewRequest(http.MethodGet, "http://example.test/archive.tar.gz", nil)

	assert.Error(t, refuseSchemeDowngrade(plain, []*http.Request{secure}))
	assert.NoError(t, refuseSchemeDowngrade(secure, []*http.Request{secure}))
	assert.NoError(t, refuseSchemeDowngrade(plain, []*http.Request{plain}), "a plain-text start was never a promise to keep")
}

func TestReleaseHTTPClientRefusesToLeaveHTTPS(t *testing.T) {
	t.Parallel()
	client := newHTTPClient(time.Second)
	require.NotNil(t, client.CheckRedirect, "both release requests build their client here")

	secure := httptest.NewRequest(http.MethodGet, "https://example.test/archive.tar.gz", nil)
	plain := httptest.NewRequest(http.MethodGet, "http://example.test/archive.tar.gz", nil)

	assert.Error(t, client.CheckRedirect(plain, []*http.Request{secure}))
}

func TestDownloadToAcceptsAResponseExactlyAtTheLimit(t *testing.T) {
	body := make([]byte, 16)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer ts.Close()
	allowAssetsFrom(t, ts.URL)
	destPath := filepath.Join(t.TempDir(), "archive.tar.gz")

	require.NoError(t, downloadTo(ts.URL, destPath, int64(len(body))), "the limit is the largest size allowed, not the first refused")

	written, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Len(t, written, len(body))
}

func TestRefuseSchemeDowngradeStopsAnEndlessRedirectChain(t *testing.T) {
	t.Parallel()
	secure := httptest.NewRequest(http.MethodGet, "https://example.test/archive.tar.gz", nil) // Replacing the default policy replaced its redirect cap too, so this one has to carry it.
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = secure
	}

	assert.Error(t, refuseSchemeDowngrade(secure, via))
	assert.NoError(t, refuseSchemeDowngrade(secure, via[:9]))
}

func TestDownloadToRefusesAnAssetOutsideThePinnedOrigins(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		assetURL string
	}{
		{name: "plain text", assetURL: "http://github.com/alpacax/alpacon-cli/releases/download/v1.4.0/alpacon.tar.gz"},
		{name: "another host entirely", assetURL: "https://evil.test/alpacon.tar.gz"},
		{name: "the pinned host as a subdomain of another", assetURL: "https://github.com.evil.test/alpacon.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destPath := filepath.Join(t.TempDir(), "archive.tar.gz")

			err := downloadTo(tt.assetURL, destPath, 1<<20)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "refusing a release asset")
			_, statErr := os.Stat(destPath)
			assert.ErrorIs(t, statErr, os.ErrNotExist, "a refused asset must not reach disk")
		})
	}
}

func TestCheckAssetOriginAcceptsThePinnedGitHubOrigins(t *testing.T) {
	t.Parallel()
	for _, origin := range allowedAssetOrigins {
		assert.NoError(t, checkAssetOrigin(origin+"/alpacax/alpacon-cli/releases/download/v1.4.0/alpacon.tar.gz"), "%s is where releases actually come from", origin)
	}
}

// url.Parse hands back the host as written, so a pin comparing it raw would
// refuse a release that is fine.
func TestCheckAssetOriginAcceptsAPinnedOriginSpelledDifferently(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"https://GitHub.com/alpacax/alpacon-cli/releases/download/v1.4.0/alpacon.tar.gz",
		"https://github.com:443/alpacax/alpacon-cli/releases/download/v1.4.0/alpacon.tar.gz",
		"HTTPS://github.com/alpacax/alpacon-cli/releases/download/v1.4.0/alpacon.tar.gz",
	} {
		assert.NoError(t, checkAssetOrigin(rawURL), rawURL)
	}
	assert.Error(t, checkAssetOrigin("https://github.com:8443/alpacax/alpacon-cli/releases/download/v1.4.0/alpacon.tar.gz"), "a non-default port is a different endpoint, not a spelling")
}
