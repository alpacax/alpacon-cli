package ftp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	assert.Zero(t, utils.HTTPStatusCode(err), "a timeout is not the throttle")
	code, _ := utils.ParseErrorResponse(err)
	assert.Empty(t, code)
}

// bulkStatusRefusalServer runs a bulk upload of two files whose status polls
// are refused with hintFor's hint for each file's transfer.
func bulkStatusRefusalServer(t *testing.T, hintFor func(id string) string) *client.AlpaconClient {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == uploadBulkAPIURL:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode([]UploadResponse{
				{ID: "id-1", Name: "a.txt", UploadURL: "http://" + r.Host + "/s3/a"},
				{ID: "id-2", Name: "b.txt", UploadURL: "http://" + r.Host + "/s3/b"},
			})
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == uploadBulkTriggerURL:
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, uploadAPIURL), "/status/")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprintf(w, `{"code": "api_token_scope_missing", "gate": "token_scope", "hint": %q}`, hintFor(id))
		}
	}))
	t.Cleanup(ts.Close)
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, Token: "test-token"}
}

func uploadTwoFiles(t *testing.T, ac *client.AlpaconClient) error {
	t.Helper()
	request := &BulkUploadRequest{Names: []string{"a.txt", "b.txt"}, Path: "/home/alice", Server: "srv-id"}
	files := []io.Reader{bytes.NewReader([]byte("a")), bytes.NewReader([]byte("b"))}
	return executeBulkUpload(ac, request, files, []int64{1, 1})
}

// A bulk upload whose status polls are refused keeps every refusal behind the
// aggregate error: its message is the list it always was, and each distinct
// hint reads through it once.
func TestExecuteBulkUpload_StatusRefusalsKeepTheirHints(t *testing.T) {
	t.Parallel()
	const hint = "This token does not hold a scope this action needs; use a token that has the scope, then retry."

	t.Run("the same hint on both files", func(t *testing.T) {
		t.Parallel()
		err := uploadTwoFiles(t, bulkStatusRefusalServer(t, func(string) string { return hint }))
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "upload failed for 2 file(s):\n  a.txt: failed to check transfer status: "), "err: %q", err.Error())
		assert.Contains(t, err.Error(), "\n  b.txt: failed to check transfer status: ")
		assert.Equal(t, hint, utils.RefusalHint(err))
		assert.Equal(t, []utils.NextAction{{Description: hint}}, utils.RefusalNextActions(err))
		// An item's code does not speak for the batch.
		code, _ := utils.ParseErrorResponse(err)
		assert.Empty(t, code)
		assert.Equal(t, http.StatusForbidden, utils.HTTPStatusCode(err), "both items agree on 403")
	})

	t.Run("a different hint on each file", func(t *testing.T) {
		t.Parallel()
		err := uploadTwoFiles(t, bulkStatusRefusalServer(t, func(id string) string { return "Hint for " + id + "." }))
		require.Error(t, err)
		assert.Equal(t, []utils.NextAction{{Description: "Hint for id-1."}, {Description: "Hint for id-2."}}, utils.RefusalNextActions(err))
	})
}

// A bulk upload in which one file meets a work-session gate is not classified
// as that gate: the work-session handler leaves it alone, so the per-file
// list and every distinct hint reach the batch's own error line.
func TestExecuteBulkUpload_WorkSessionRefusalStaysInTheBatch(t *testing.T) {
	const gateHint = "This work session does not cover this server; use a session that does."
	const scopeHint = "This token does not hold a scope this action needs; use a token that has the scope, then retry."
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == uploadBulkAPIURL:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode([]UploadResponse{
				{ID: "id-1", Name: "a.txt", UploadURL: "http://" + r.Host + "/s3/a"},
				{ID: "id-2", Name: "b.txt", UploadURL: "http://" + r.Host + "/s3/b"},
			})
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == uploadBulkTriggerURL:
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/id-1/status/"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprintf(w, `{"code": %q, "source": "webftp", "hint": %q}`, utils.WorkSessionServerNotAllowed, gateHint)
		case strings.HasSuffix(r.URL.Path, "/id-2/status/"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprintf(w, `{"code": "api_token_scope_missing", "gate": "token_scope", "hint": %q}`, scopeHint)
		}
	}))
	defer ts.Close()

	err := uploadTwoFiles(t, &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, Token: "test-token"})
	require.Error(t, err)
	code, _ := utils.ParseErrorResponse(err)
	assert.Empty(t, code)

	_, stderr := testutil.CaptureOutput(t, func() {
		// Returns: a batch is not a work-session refusal. It would exit 3 otherwise.
		utils.HandleWorkSessionError(err, utils.OperationWebFTP, "my-server", "API token", "")
		utils.CliError("Failed to upload to '%s': %s", "my-server:/home/alice", err)
	})
	assert.Contains(t, stderr, "upload failed for 2 file(s):\n  a.txt: ")
	assert.Contains(t, stderr, "\n  b.txt: ")
	assert.Equal(t, 1, strings.Count(stderr, "Hint: "+gateHint), "stderr: %q", stderr)
	assert.Equal(t, 1, strings.Count(stderr, "Hint: "+scopeHint), "stderr: %q", stderr)
	assert.NotContains(t, stderr, "Next:")
}
