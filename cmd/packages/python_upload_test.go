package packages

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPythonPackageUploadSendsTheFile(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		received []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		part, err := r.MultipartReader()
		if r.Method != http.MethodPost || r.URL.Path != "/api/packages/python/entries/" || err != nil {
			t.Errorf("unexpected request: %s %s (%v)", r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		file, err := part.NextPart()
		if err != nil {
			t.Errorf("no file part: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(file)
		mu.Lock()
		received = append(received, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	wheel := filepath.Join(t.TempDir(), "alpamon-1.1.0-py3-none-any.whl")
	require.NoError(t, os.WriteFile(wheel, []byte("wheel-content"), 0o600))

	failure, code := cmdtest.Run(newPythonPackageUploadCmd(cmdtest.ClientFactory(ts), utils.Stdio), wheel)

	assert.Equal(t, 0, code, failure)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"wheel-content"}, received)
}

func TestPythonPackageUploadFails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exists   bool
		wantHits int
	}{
		{name: "missing file", exists: false, wantHits: 0},
		{name: "server refuses", exists: true, wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"POST /api/packages/python/entries/": {Status: http.StatusBadRequest, Body: `{"content": ["invalid wheel"]}`},
			})
			wheel := filepath.Join(t.TempDir(), "pkg.whl")
			if tt.exists {
				require.NoError(t, os.WriteFile(wheel, []byte("x"), 0o600))
			}

			failure, code := cmdtest.Run(newPythonPackageUploadCmd(ts.Factory(), utils.Stdio), wheel)

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, "Failed to upload the Python package to Alpacon")
			assert.Equal(t, tt.wantHits, ts.Hits("POST /api/packages/python/entries/"))
		})
	}
}

func TestPythonPackageUploadRefusesAMissingArgumentWithExit2(t *testing.T) {
	t.Parallel()

	failure, code := cmdtest.Run(newPythonPackageUploadCmd(cmdtest.NoClient, utils.Stdio))

	assert.Equal(t, utils.ExitCodeUsageError, code)
	assert.Contains(t, failure, "accepts 1 arg(s), received 0")
}
