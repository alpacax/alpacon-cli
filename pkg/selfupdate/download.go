package selfupdate

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

// maxChecksumsSize bounds the checksums file, which is a few lines of text.
// Nothing authenticates a download until those checksums are compared, so an
// endless stream would otherwise fill the temp directory—memory, on tmpfs.
const maxChecksumsSize = 1 << 20

// maxRedirects replaces the cap the default CheckRedirect carried, which
// setting one of our own removed.
const maxRedirects = 10

// archiveDownloadTimeout covers a whole transfer rather than a stalled byte, so
// it has to clear the largest release archive on a poor connection. It exists
// to stop a hung download, not to pace a slow one.
const archiveDownloadTimeout = 10 * time.Minute

// The release JSON names both asset URLs, and refuseSchemeDowngrade only
// inspects a redirect—so an http:// or off-GitHub URL would be fetched as
// given. Later hops stay unpinned: GitHub's CDN host changes.
var allowedAssetOrigins = []string{
	"https://github.com",
	"https://objects.githubusercontent.com",
	"https://release-assets.githubusercontent.com",
}

func FetchVerifiedBinary(release *Release, goos, goarch, binaryName, destDir string) (string, error) {
	archiveName := ArchiveName(release.Version, goos, goarch)
	archiveAsset, err := SelectAsset(release, archiveName)
	if err != nil {
		return "", err
	}
	checksumsName := ChecksumsName(release.Version)
	checksumsAsset, err := SelectAsset(release, checksumsName)
	if err != nil {
		return "", err
	}

	archivePath := filepath.Join(destDir, archiveName)
	if err := downloadTo(archiveAsset.DownloadURL, archivePath, maxReleaseFileSize); err != nil {
		return "", err
	}

	checksumsPath := filepath.Join(destDir, checksumsName)
	if err := downloadTo(checksumsAsset.DownloadURL, checksumsPath, maxChecksumsSize); err != nil {
		return "", err
	}
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return "", err
	}

	if err := VerifyChecksum(archivePath, ParseChecksums(checksums)[archiveName]); err != nil {
		return "", err
	}
	return ExtractBinary(archivePath, binaryName, destDir)
}

// assetOrigin normalizes what url.Parse leaves exactly as written: the host
// keeps its case, and an explicit :443 stays on. Both name the same origin as
// the pinned entry, and comparing them raw would refuse a release that is fine.
func assetOrigin(parsed *url.URL) string {
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Host)
	if scheme == "https" {
		host = strings.TrimSuffix(host, ":443")
	}
	return scheme + "://" + host
}

func checkAssetOrigin(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("release asset url %q cannot be read: %w", rawURL, err)
	}
	origin := assetOrigin(parsed)
	if !slices.Contains(allowedAssetOrigins, origin) {
		return fmt.Errorf("refusing a release asset served from %s", origin)
	}
	return nil
}

func downloadTo(assetURL, destPath string, maxBytes int64) error {
	if err := checkAssetOrigin(assetURL); err != nil {
		return err
	}

	client := newHTTPClient(archiveDownloadTimeout)
	req, err := http.NewRequest(http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", utils.GetUserAgent())

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s returned %s", assetURL, resp.Status)
	}

	file, err := os.Create(destPath)
	if err != nil {
		return err
	}
	overLimit, err := writeBounded(file, resp.Body, maxBytes)
	if err != nil {
		return err
	}
	if overLimit {
		return fmt.Errorf("downloading %s exceeded %d bytes", assetURL, maxBytes)
	}
	return nil
}

// writeBounded copies src into file, closes it, and reports whether the stream
// ran past maxBytes. Reading one byte more than the bound is what separates a
// stream that overran it from one landing exactly on it, so the +1 and the >
// are a single decision and belong in a single place.
func writeBounded(file *os.File, src io.Reader, maxBytes int64) (bool, error) {
	written, copyErr := io.Copy(file, io.LimitReader(src, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return false, copyErr
	}
	if written > maxBytes {
		return true, nil
	}
	return false, closeErr
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: refuseSchemeDowngrade}
}

// refuseSchemeDowngrade stops a redirect from https to http: the archive and
// the checksums that vouch for it come down the same connection, so a redirect
// into plain text would let whoever wrote it replace both together.
func refuseSchemeDowngrade(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect from https to %s", req.URL.Scheme)
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	return nil
}
