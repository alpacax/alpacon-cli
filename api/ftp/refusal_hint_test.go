package ftp

import (
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

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

// A 429 that outlasts the download retries still reaches the caller with its
// code, status and hint; the retries themselves are unchanged.
func TestDownloadFileToPath_ExhaustedThrottleKeepsTheHint(t *testing.T) {
	restore := swapDownloadRetryDelays(time.Millisecond, time.Millisecond)
	defer restore()
	const hint = "Too many requests were made in a short time; wait, then retry."

	ac := refusedBlobClient(http.StatusTooManyRequests, `{"code": "api_rate_limited", "gate": "rate", "hint": "`+hint+`"}`)
	_, err := DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "download failed after 10 attempts (last status: 429)")
	assert.Equal(t, hint, utils.RefusalHint(err))
	assert.Equal(t, http.StatusTooManyRequests, utils.HTTPStatusCode(err))
	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "api_rate_limited", code)

	ac = refusedBlobClient(http.StatusTooManyRequests, `{"code": "api_rate_limited"}`)
	_, err = DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "download failed after 10 attempts (last status: 429)")
	assert.Empty(t, utils.RefusalHint(err))
}

// A status poll that times out while throttled keeps the last 429, hint
// included, behind the same timeout message.
func TestPollTransferStatus_TimeoutWhileThrottledKeepsTheHint(t *testing.T) {
	const hint = "Too many requests were made in a short time; wait, then retry."
	ac := &client.AlpaconClient{
		BaseURL: testutil.StubBaseURL,
		HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
			return http.StatusTooManyRequests, `{"code": "api_rate_limited", "gate": "rate", "hint": "` + hint + `"}`
		}),
	}

	var err error
	_, stderr := testutil.CaptureOutput(t, func() {
		synctest.Test(t, func(t *testing.T) {
			_, _, err = pollTransferStatus(ac, downloadTransfer, testPathID(t, "t-1"), 2*time.Second, 100*time.Millisecond)
		})
	})
	require.Error(t, err, "stderr: %s", stderr)
	assert.Equal(t, "transfer status polling timed out after 2s", err.Error())
	assert.Equal(t, hint, utils.RefusalHint(err))
}
