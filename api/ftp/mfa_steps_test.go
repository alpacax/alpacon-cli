package ftp

import (
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stepStub accepts every create and refuses one named later request for MFA a
// set number of times before it answers normally, counting each request, so a
// test can tell a retried request from a recreated transfer.
type stepStub struct {
	refuseMethod string
	refusePath   string
	refusals     int32 // how many times the refused request is refused; negative: always

	creates, refusedHits, otherHits atomic.Int32
	blob                            string
}

func (s *stepStub) client() *client.AlpaconClient {
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			if r.Method == s.refuseMethod && r.URL.Path == s.refusePath {
				n := s.refusedHits.Add(1)
				if s.refusals < 0 || n <= s.refusals {
					return http.StatusForbidden, mfaRefusal
				}
			}
			switch {
			case r.URL.Path == "/api/auth0/mfa/":
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
			case r.Method == http.MethodPost && (r.URL.Path == uploadAPIURL || r.URL.Path == downloadAPIURL):
				s.creates.Add(1)
				return http.StatusCreated, `{"id": "t-1", "command": "cmd-1", "download_url": "http://stub.invalid/blob"}`
			case r.Method == http.MethodPost && r.URL.Path == uploadBulkAPIURL:
				s.creates.Add(1)
				return http.StatusCreated, `[{"id": "t-1"}, {"id": "t-2"}]`
			case r.URL.Path == "/api/events/commands/cmd-1/":
				return http.StatusOK, `{"id": "cmd-1", "status": "completed", "success": true}`
			case r.URL.Path == "/blob":
				if r.Method == s.refuseMethod && r.URL.Path == s.refusePath {
					return http.StatusOK, s.blob
				}
				s.otherHits.Add(1)
				return http.StatusOK, s.blob
			case r.URL.Path == "/api/webftp/uploads/t-1/status/", r.URL.Path == "/api/webftp/uploads/t-2/status/",
				r.URL.Path == "/api/webftp/downloads/t-1/status/":
				return http.StatusOK, `{"success": true, "message": "done"}`
			default:
				// Upload triggers.
				return http.StatusOK, `{}`
			}
		}),
	}
}

// After MFA only the refused request is sent again: the transfer the server
// already created is not created a second time.
func TestTransfer_MFARefusalAfterTheCreateRetriesOnlyTheRefusedRequest(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name     string
		stub     func(t *testing.T) *stepStub
		run      func(t *testing.T, ac *client.AlpaconClient) error
		wantHits int32
	}{
		{
			name: "upload trigger",
			stub: func(*testing.T) *stepStub {
				return &stepStub{refuseMethod: http.MethodGet, refusePath: "/api/webftp/uploads/t-1/upload/", refusals: 1}
			},
			run: func(t *testing.T, ac *client.AlpaconClient) error {
				t.Helper()
				return UploadLocalFileAs(ac, writeTempFile(t, "a.txt"), "my-server", "/home/alice/a.txt", "", "", "")
			},
			wantHits: 2,
		},
		{
			name: "upload status poll",
			stub: func(*testing.T) *stepStub {
				return &stepStub{refuseMethod: http.MethodGet, refusePath: "/api/webftp/uploads/t-1/status/", refusals: 1}
			},
			run: func(t *testing.T, ac *client.AlpaconClient) error {
				t.Helper()
				return UploadLocalFileAs(ac, writeTempFile(t, "a.txt"), "my-server", "/home/alice/a.txt", "", "", "")
			},
			wantHits: 3, // refused, the wait's one-request probe, then the poll
		},
		{
			name: "bulk upload trigger",
			stub: func(*testing.T) *stepStub {
				return &stepStub{refuseMethod: http.MethodPost, refusePath: uploadBulkTriggerURL, refusals: 1}
			},
			run: func(t *testing.T, ac *client.AlpaconClient) error {
				t.Helper()
				return UploadFile(ac, []string{writeTempFile(t, "a.txt"), writeTempFile(t, "b.txt")}, "my-server:/home/alice/", "", "", false, "")
			},
			wantHits: 2,
		},
		{
			name: "download status poll",
			stub: func(*testing.T) *stepStub {
				return &stepStub{refuseMethod: http.MethodGet, refusePath: "/api/webftp/downloads/t-1/status/", refusals: 1, blob: "content"}
			},
			run: func(t *testing.T, ac *client.AlpaconClient) error {
				t.Helper()
				_, err := DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
				return err
			},
			wantHits: 3, // refused, the wait's one-request probe, then the poll
		},
		{
			name: "download fetch",
			stub: func(*testing.T) *stepStub {
				return &stepStub{refuseMethod: http.MethodGet, refusePath: "/blob", refusals: 1, blob: "content"}
			},
			run: func(t *testing.T, ac *client.AlpaconClient) error {
				t.Helper()
				_, err := DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
				return err
			},
			wantHits: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := tc.stub(t)
			ac := stub.client()

			var err error
			synctest.Test(t, func(t *testing.T) {
				err = tc.run(t, ac)
			})

			require.NoError(t, err)
			assert.Equal(t, int32(1), stub.creates.Load(), "the transfer must be created once")
			assert.Equal(t, tc.wantHits, stub.refusedHits.Load(), "the refused request: once refused, once retried")
		})
	}
}

// A wait that ends without the refused status poll getting through cannot say
// how the transfer ended, and the message says so.
func TestTransfer_StatusPollRefusedForGoodSaysTheTransferMayHaveCompleted(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	stub := &stepStub{refuseMethod: http.MethodGet, refusePath: "/api/webftp/uploads/t-1/status/", refusals: -1}
	ac := stub.client()

	var err error
	synctest.Test(t, func(t *testing.T) {
		err = UploadLocalFileAs(ac, writeTempFile(t, "a.txt"), "my-server", "/home/alice/a.txt", "", "", "")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "may already have completed")
	assert.Equal(t, int32(1), stub.creates.Load())
}
