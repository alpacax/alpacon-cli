package ftp

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A folder is zipped only once the server accepts the create request, so the
// attempts an MFA step-up refuses cost no zip. An unreadable folder shows the
// order: zipping first would fail on it, while the refusal comes first when the
// request goes first.
func TestUploadFolder_RefusedCreateZipsNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this process cannot read")
	}
	// Listable at the top, so the up-front check passes, but not below it.
	folder := t.TempDir()
	inner := filepath.Join(folder, "inner")
	require.NoError(t, os.Mkdir(inner, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(inner, "f"), []byte("x"), 0o600))
	require.NoError(t, os.Chmod(inner, 0o000))
	t.Cleanup(func() { _ = os.Chmod(inner, 0o700) })

	for name, src := range map[string][]string{"single": {folder}, "bulk": {folder, folder}} {
		t.Run(name, func(t *testing.T) {
			ac := &client.AlpaconClient{
				BaseURL:       testutil.StubBaseURL,
				WorkspaceName: "my-workspace",
				HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
					if r.Method == http.MethodPost {
						return http.StatusForbidden, mfaRefusal
					}
					return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
				}),
			}

			err := UploadFolder(ac, src, "my-server:/tmp/", "", "", false, "")

			require.Error(t, err)
			assert.Equal(t, utils.AuthMFARequired, utils.StructuredErrorCode(err), "the refusal, not a zip failure: %v", err)
		})
	}
}

// A folder that does not exist fails before the server is asked for anything.
func TestUploadFolder_MissingFolderSendsNoCreate(t *testing.T) {
	var posts atomic.Int32
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			if r.Method == http.MethodPost {
				posts.Add(1)
			}
			return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
		}),
	}

	err := UploadFolder(ac, []string{filepath.Join(t.TempDir(), "nope")}, "my-server:/tmp/", "", "", false, "")

	require.ErrorContains(t, err, "no such file or directory")
	assert.Zero(t, posts.Load())
}

// A folder that cannot be listed fails before the server is asked for anything,
// so no upload slot is left orphaned.
func TestUploadFolder_UnreadableFolderSendsNoCreate(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this process cannot read")
	}
	folder := filepath.Join(t.TempDir(), "locked")
	require.NoError(t, os.Mkdir(folder, 0o700))
	require.NoError(t, os.Chmod(folder, 0o000))
	t.Cleanup(func() { _ = os.Chmod(folder, 0o700) })
	var posts atomic.Int32
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			if r.Method == http.MethodPost {
				posts.Add(1)
			}
			return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
		}),
	}

	err := UploadFolder(ac, []string{folder}, "my-server:/tmp/", "", "", false, "")

	require.ErrorContains(t, err, "permission denied")
	assert.Zero(t, posts.Load())
}
