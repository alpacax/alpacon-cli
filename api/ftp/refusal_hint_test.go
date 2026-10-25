package ftp

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refusedBlobClient serves a download whose final fetch is refused with blob.
func refusedBlobClient(status int, blob string) *client.AlpaconClient {
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
			case r.Method == http.MethodPost && r.URL.Path == downloadAPIURL:
				return http.StatusCreated, `{"id": "t-1", "command": "cmd-1", "download_url": "http://stub.invalid/blob"}`
			case r.URL.Path == "/api/events/commands/cmd-1/":
				return http.StatusOK, `{"id": "cmd-1", "status": "completed", "success": true}`
			case r.URL.Path == "/api/webftp/downloads/t-1/status/":
				return http.StatusOK, `{"success": true, "message": "done"}`
			case r.URL.Path == "/blob":
				return status, blob
			default:
				return http.StatusOK, `{}`
			}
		}),
	}
}

// The final fetch does not go through the shared client parser, so its error
// reads the hint itself; without one it is what it was.
func TestDownloadFileToPath_FetchRefusalKeepsTheHint(t *testing.T) {
	t.Parallel()
	const hint = "This token's file access rules do not allow this transfer for this path, account and direction."

	ac := refusedBlobClient(http.StatusForbidden, `{"code": "api_token_acl_not_allowed", "gate": "file_acl", "hint": "`+hint+`\n"}`)
	_, err := DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
	require.Error(t, err)
	assert.Equal(t, hint, utils.RefusalHint(err))
	assert.Equal(t, http.StatusForbidden, utils.HTTPStatusCode(err))
	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "api_token_acl_not_allowed", code)

	ac = refusedBlobClient(http.StatusForbidden, `{"code": "api_token_acl_not_allowed"}`)
	_, err = DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
	require.Error(t, err)
	assert.Empty(t, utils.RefusalHint(err))
}
