package selfupdate

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

// maxChecksumsSize bounds the checksums file, which is a few lines of text.
// Nothing authenticates a download until those checksums are compared, so an
// endless stream would otherwise fill the temp directory—memory, on tmpfs.
const maxChecksumsSize = 1 << 20

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
	checksumsAsset, err := SelectAsset(release, ChecksumsName(release.Version))
	if err != nil {
		return "", err
	}

	archivePath := filepath.Join(destDir, archiveName)
	if err := downloadTo(archiveAsset.DownloadURL, archivePath, maxReleaseFileSize); err != nil {
		return "", err
	}

	checksumsPath := filepath.Join(destDir, ChecksumsName(release.Version))
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

func checkAssetOrigin(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("release asset url %q cannot be read: %w", rawURL, err)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	if !slices.Contains(allowedAssetOrigins, origin) {
		return fmt.Errorf("refusing a release asset served from %s", origin)
	}
	return nil
}

func downloadTo(assetURL, destPath string, maxBytes int64) error {
	if err := checkAssetOrigin(assetURL); err != nil {
		return err
	}

	client := newHTTPClient(10 * time.Minute)
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
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if written > maxBytes {
		return fmt.Errorf("downloading %s exceeded %d bytes", assetURL, maxBytes)
	}
	return closeErr
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
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}
