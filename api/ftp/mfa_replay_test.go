package ftp

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An upload or download the server accepted must not be submitted again: after
// the MFA refusal the retry is accepted, and a 503 on the transfer status that
// follows is a failure to learn the outcome, not a refused submission.
func TestMFARetry_DoesNotResubmitAnAcceptedTransferWhenTheStatusCheckFails(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())

	local := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(local, []byte("data"), 0o600))

	tests := []struct {
		name string
		run  func(ac *client.AlpaconClient) error
	}{
		{"upload", func(ac *client.AlpaconClient) error {
			return UploadLocalFileAs(ac, local, "my-server", "/tmp/f.txt", "", "", "")
		}},
		{"download", func(ac *client.AlpaconClient) error {
			_, err := DownloadFileToPath(ac, "my-server", "/tmp/f.txt", filepath.Join(t.TempDir(), "out.txt"), "", "", "")
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var posts, accepted atomic.Int32
			ac := &client.AlpaconClient{
				BaseURL:       testutil.StubBaseURL,
				WorkspaceName: "my-workspace",
				HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
					switch {
					case r.Method == http.MethodPost && (r.URL.Path == uploadAPIURL || r.URL.Path == downloadAPIURL):
						if posts.Add(1) == 1 {
							return http.StatusForbidden, `{"code": "auth_mfa_required", "source": "server"}`
						}
						accepted.Add(1)
						return http.StatusCreated, `{"id": "tr-1", "command": "cmd-1", "upload_url": "", "download_url": "http://stub.invalid/blob"}`
					case r.URL.Path == "/blob":
						return http.StatusOK, `data`
					case r.URL.Path == "/api/webftp/uploads/tr-1/upload/":
						return http.StatusOK, `{}`
					case r.URL.Path == "/api/events/commands/cmd-1/":
						return http.StatusOK, `{"id": "cmd-1", "status": "completed", "success": true}`
					case r.URL.Path == "/api/webftp/uploads/tr-1/status/", r.URL.Path == "/api/webftp/downloads/tr-1/status/":
						return http.StatusServiceUnavailable, `{"code": "api_search_unavailable"}`
					case r.URL.Path == "/api/auth0/mfa/":
						return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
					case r.URL.Path == "/api/servers/servers/":
						return http.StatusOK, `{"count": 1, "results": [{"id": "server-id"}]}`
					}
					return http.StatusNotFound, `{}`
				}),
			}

			var err error
			synctest.Test(t, func(t *testing.T) {
				err = tc.run(ac)
				require.Error(t, err)
				err = utils.HandleCommonErrors(err, "my-server", mfa.ErrorCallbacks(ac, func() error { return tc.run(ac) }))
			})

			require.Error(t, err, "the failed status check must reach the user")
			assert.Equal(t, int32(1), accepted.Load(), "an accepted transfer must not be submitted again")
		})
	}
}

// A folder is zipped only once the server accepts the upload, so the attempts an
// MFA step-up refuses cost no zip. An unreadable folder shows the order: zipping
// first would fail on it, the refusal comes first when the request goes first.
func TestUploadFolder_RefusedRequestZipsNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this process cannot read")
	}
	folder := filepath.Join(t.TempDir(), "locked")
	require.NoError(t, os.Mkdir(folder, 0o700))
	require.NoError(t, os.Chmod(folder, 0o000))
	t.Cleanup(func() { _ = os.Chmod(folder, 0o700) })

	for name, src := range map[string][]string{"single": {folder}, "bulk": {folder, folder}} {
		t.Run(name, func(t *testing.T) {
			ac := &client.AlpaconClient{
				BaseURL:       testutil.StubBaseURL,
				WorkspaceName: "my-workspace",
				HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
					if r.Method == http.MethodPost {
						return http.StatusForbidden, `{"code": "auth_mfa_required", "source": "server"}`
					}
					return http.StatusOK, `{"count": 1, "results": [{"id": "server-id"}]}`
				}),
			}

			err := UploadFolder(ac, src, "my-server:/tmp/", "", "", false, "")

			require.Error(t, err)
			assert.Equal(t, utils.AuthMFARequired, utils.ErrorCodeOf(err), "the refusal, not a zip failure: %v", err)
		})
	}
}
