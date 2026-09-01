// Package selfupdate replaces the running binary with the latest release.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

// DefaultReleaseAPIURL is a var so a test can point a real alpacon process at a
// stub. Exit code 7 is only observable in a process that actually exits, and a
// child process cannot be handed a different endpoint any other way.
var DefaultReleaseAPIURL = "https://api.github.com/repos/alpacax/alpacon-cli/releases/latest"

// The version goes straight into the asset names and from there into a path
// under the temp directory, so releaseTagPattern refuses a tag carrying a
// separator, or nothing at all, rather than turning it into a filename.
var releaseTagPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]*$`)

type Asset struct {
	Name        string
	DownloadURL string
}

type Release struct {
	Version string
	HTMLURL string
	Assets  []Asset
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func LatestRelease(apiURL string) (*Release, error) {
	client := newHTTPClient(5 * time.Second) // Five seconds is what 'alpacon version' already waited: one small JSON fetch, on a client that never sees the archive download.
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", utils.GetUserAgent())

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned %s", resp.Status)
	}

	var raw githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, err
	}

	version := strings.TrimPrefix(raw.TagName, "v")
	if !releaseTagPattern.MatchString(version) {
		return nil, fmt.Errorf("github api returned an unusable release tag %q", raw.TagName)
	}

	release := &Release{
		Version: version,
		HTMLURL: raw.HTMLURL,
	}
	for _, a := range raw.Assets {
		release.Assets = append(release.Assets, Asset{Name: a.Name, DownloadURL: a.DownloadURL})
	}
	return release, nil
}
