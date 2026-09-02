package selfupdate

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLatestReleaseStripsTagPrefixAndReadsAssets(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/vnd.github.v3+json", r.Header.Get("Accept"))
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.4.0",
			"html_url": "https://example.test/notes",
			"assets": [
				{"name": "alpacon-1.4.0-linux-amd64.tar.gz", "browser_download_url": "https://example.test/a.tar.gz"},
				{"name": "alpacon-1.4.0-checksums.sha256", "browser_download_url": "https://example.test/sums"}
			]
		}`))
	}))
	defer ts.Close()

	release, err := LatestRelease(ts.URL)

	require.NoError(t, err)
	assert.Equal(t, "1.4.0", release.Version)
	assert.Equal(t, "https://example.test/notes", release.HTMLURL)
	require.Len(t, release.Assets, 2)
	assert.Equal(t, "alpacon-1.4.0-linux-amd64.tar.gz", release.Assets[0].Name)
	assert.Equal(t, "https://example.test/a.tar.gz", release.Assets[0].DownloadURL)
}

func TestLatestReleaseRejectsABodyThatIsNotJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>blocked by a proxy</html>"))
	}))
	defer ts.Close()

	_, err := LatestRelease(ts.URL)

	require.Error(t, err)
}

func TestLatestReleaseRejectsNonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	_, err := LatestRelease(ts.URL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestLatestReleaseRejectsATagItCannotTurnIntoFileNames(t *testing.T) {
	tests := []struct {
		name string
		tag  string
	}{
		{name: "no tag at all", tag: ""},
		{name: "only the v prefix", tag: "v"},
		{name: "a path separator", tag: "v../../etc/passwd"},
		{name: "a space", tag: "v1.4.0 latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"tag_name": %q, "html_url": "https://example.test/notes"}`, tt.tag)
			}))
			defer ts.Close()

			_, err := LatestRelease(ts.URL)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "unusable release tag")
		})
	}
}
