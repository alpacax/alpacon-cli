package selfupdate

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
