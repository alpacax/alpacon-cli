package ftp

import (
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mfaRefusal = `{"code": "auth_mfa_required", "source": "webftp"}`

// transferStub refuses the first create (single or bulk) for MFA and accepts
// the rest. Every read after an accepted create answers 503, the answer that
// would also mean "not processed" had it come from the create itself.
type transferStub struct {
	creates atomic.Int32
}

func (s *transferStub) client() *client.AlpaconClient {
	unavailable := func() (int, string) {
		return http.StatusServiceUnavailable, `{"detail": "unavailable"}`
	}
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
			case r.Method == http.MethodPost && (r.URL.Path == uploadAPIURL || r.URL.Path == downloadAPIURL):
				if s.creates.Add(1) == 1 {
					return http.StatusForbidden, mfaRefusal
				}
				return http.StatusCreated, `{"id": "t-1", "command": "cmd-1", "download_url": "http://stub.invalid/blob"}`
			case r.Method == http.MethodPost && r.URL.Path == uploadBulkAPIURL:
				if s.creates.Add(1) == 1 {
					return http.StatusForbidden, mfaRefusal
				}
				return http.StatusCreated, `[{"id": "t-1"}, {"id": "t-2"}]`
			case r.URL.Path == "/api/webftp/uploads/t-1/upload/" || r.URL.Path == uploadBulkTriggerURL:
				return http.StatusOK, `{}`
			case r.URL.Path == "/api/events/commands/cmd-1/":
				return http.StatusOK, `{"id": "cmd-1", "status": "completed", "success": true}`
			case r.URL.Path == "/blob":
				return http.StatusOK, `content`
			default:
				// The transfer status.
				return unavailable()
			}
		}),
	}
}

// acceptedCreates is the number of creates the server accepted: all but the
// first, which it refused for MFA.
func (s *transferStub) acceptedCreates() int32 { return s.creates.Load() - 1 }

func writeTempFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("content"), 0o600))
	return path
}

// mfaWait runs op, and on its MFA refusal hands it to the MFA wait the way the
// cp and edit commands do.
func mfaWait(t *testing.T, op func() error) error {
	t.Helper()
	first := op()
	code, _ := utils.ParseErrorResponse(first)
	require.Equal(t, utils.AuthMFARequired, code, "got %v", first)
	return utils.HandleCommonErrors(first, "my-server", utils.ErrorHandlerCallbacks{
		OnMFARequired:  func(string) error { return nil },
		RetryOperation: op,
	})
}

// An upload the server accepted has been handed to the server. A status poll
// that fails after it is the upload's result: the MFA wait must not take it for
// a refused create and upload the file a second time.
func TestUpload_MFAWaitDoesNotUploadAgainAfterAnAcceptedUpload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		upload func(ac *client.AlpaconClient, files []string) error
		files  int
	}{
		{
			name: "edit single file",
			upload: func(ac *client.AlpaconClient, files []string) error {
				return UploadLocalFileAs(ac, files[0], "my-server", "/home/alice/a.txt", "", "", "")
			},
			files: 1,
		},
		{
			name: "cp single file",
			upload: func(ac *client.AlpaconClient, files []string) error {
				return UploadFile(ac, files, "my-server:/home/alice/", "", "", false, "")
			},
			files: 1,
		},
		{
			name: "cp bulk",
			upload: func(ac *client.AlpaconClient, files []string) error {
				return UploadFile(ac, files, "my-server:/home/alice/", "", "", false, "")
			},
			files: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var files []string
			for i := range tc.files {
				files = append(files, writeTempFile(t, string(rune('a'+i))+".txt"))
			}
			stub := &transferStub{}
			ac := stub.client()

			var err error
			synctest.Test(t, func(t *testing.T) {
				err = mfaWait(t, func() error { return tc.upload(ac, files) })
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to check transfer status")
			assert.Equal(t, int32(1), stub.acceptedCreates(), "the file must be uploaded once")
		})
	}
}

// The same holds for a download: once the server accepted it and the file came
// down, a status poll that fails ends the wait instead of starting a second
// download.
func TestDownload_MFAWaitDoesNotDownloadAgainAfterAnAcceptedDownload(t *testing.T) {
	t.Parallel()
	stub := &transferStub{}
	ac := stub.client()
	localPath := filepath.Join(t.TempDir(), "a.txt")

	var err error
	synctest.Test(t, func(t *testing.T) {
		err = mfaWait(t, func() error {
			_, err := DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", localPath, "", "", "")
			return err
		})
	})

	require.ErrorContains(t, err, "download transfer status check failed")
	assert.Equal(t, int32(1), stub.acceptedCreates(), "the file must be downloaded once")
}
