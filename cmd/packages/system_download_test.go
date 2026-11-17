package packages

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const debID = "550e8400-e29b-41d4-a716-446655440000"

func TestSystemPackageDownloadSavesTheFile(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/packages/system/entries/":
			_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "` + debID + `", "name": "osquery.deb"}]}`))
		case "/api/packages/system/entries/" + debID + "/":
			_, _ = w.Write([]byte(`{"download_url": "http://` + r.Host + `/api/download/osquery.deb"}`))
		case "/api/download/osquery.deb":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("deb-content"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)
	dest := t.TempDir()

	failure, code := cmdtest.Run(newSystemPackageDownloadCmd(cmdtest.ClientFactory(ts)), "osquery.deb", dest)

	assert.Equal(t, 0, code, failure)
	content, err := os.ReadFile(filepath.Join(dest, "osquery.deb"))
	require.NoError(t, err)
	assert.Equal(t, "deb-content", string(content))
}

func TestSystemPackageDownloadFailsOnAnUnknownPackage(t *testing.T) {
	t.Parallel()
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/packages/system/entries/": {Status: http.StatusOK, Body: `{"count": 0, "results": []}`},
	})
	dest := t.TempDir()

	failure, code := cmdtest.Run(newSystemPackageDownloadCmd(ts.Factory()), "absent.deb", dest)

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.Contains(t, failure, "Failed to download the system package from Alpacon")
	assert.NoFileExists(t, filepath.Join(dest, "absent.deb"))
}
