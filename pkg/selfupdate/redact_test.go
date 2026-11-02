package selfupdate

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An asset URL can carry a signed query, and none of the download errors may print it.
func TestDownloadErrorsOmitTheSignedQuery(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("0123456789abcdefghij"))
	}))
	t.Cleanup(ts.Close)
	allowAssetsFrom(t, ts.URL)

	const query = "?X-Amz-Signature=secret123&X-Amz-Credential=cred"
	cases := map[string]string{
		"status":     ts.URL + "/missing" + query,
		"size limit": ts.URL + "/archive" + query,
	}
	for name, assetURL := range cases {
		t.Run(name, func(t *testing.T) {
			err := downloadTo(assetURL, filepath.Join(t.TempDir(), "out"), 4)

			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret123")
			assert.NotContains(t, err.Error(), "X-Amz-Signature")
		})
	}
}

// A redirect chain the client stops keeps its 3xx on the error, behind the redaction.
func TestReleaseFetchesKeepARefusedRedirectStatus(t *testing.T) {
	const query = "?X-Amz-Signature=secret123&X-Amz-Credential=cred"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+query, http.StatusFound)
	}))
	t.Cleanup(ts.Close)
	allowAssetsFrom(t, ts.URL)

	tests := []struct {
		name     string
		fetch    func() error
		redacted bool // only asset URLs point at signed storage
	}{
		{"asset download", func() error { return downloadTo(ts.URL+"/archive", filepath.Join(t.TempDir(), "out"), 4) }, true},
		{"release lookup", func() error { _, err := LatestRelease(ts.URL + "/latest"); return err }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fetch()

			require.Error(t, err)
			assert.Equal(t, http.StatusFound, utils.HTTPStatusCode(err))
			if tt.redacted {
				assert.NotContains(t, err.Error(), "secret123")
			}
		})
	}
}
