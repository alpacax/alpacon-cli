package ftp

import (
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lapseStub answers each request to one path from a script of statuses, in
// order, and counts MFA links and creates, so a test can tell a new wait from
// a resent request.
type lapseStub struct {
	path   string
	method string
	script []int // status per hit on path; the last repeats

	hits, links, creates, other atomic.Int32
	blob                        string
	okBody                      string // body of a passing answer, when not the default
}

func (s *lapseStub) client() *client.AlpaconClient {
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			if r.Method == s.method && r.URL.Path == s.path {
				n := int(s.hits.Add(1)) - 1
				code := s.script[min(n, len(s.script)-1)]
				if code == http.StatusForbidden {
					return code, mfaRefusal
				}
				if r.URL.Path == "/blob" {
					return code, s.blob
				}
				if s.okBody != "" {
					return code, s.okBody
				}
				return code, `{"success": true, "message": "done"}`
			}
			switch {
			case r.URL.Path == "/api/auth0/mfa/":
				s.links.Add(1)
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
			case r.Method == http.MethodPost && (r.URL.Path == uploadAPIURL || r.URL.Path == downloadAPIURL):
				s.creates.Add(1)
				return http.StatusCreated, `{"id": "t-1", "command": "cmd-1", "download_url": "http://stub.invalid/blob"}`
			case r.URL.Path == "/api/events/commands/cmd-1/":
				return http.StatusOK, `{"id": "cmd-1", "status": "completed", "success": true}`
			case r.URL.Path == "/blob":
				s.other.Add(1)
				return http.StatusOK, s.blob
			case r.URL.Path == "/api/webftp/uploads/t-1/status/", r.URL.Path == "/api/webftp/downloads/t-1/status/":
				return http.StatusOK, `{"success": true, "message": "done"}`
			default:
				return http.StatusOK, `{}`
			}
		}),
	}
}

const (
	stOK  = http.StatusOK
	stMFA = http.StatusForbidden
)

// MFA that lapses again while the step runs again after a wait gets a wait of
// its own, whichever request ran the first wait, and nothing the server
// already acted on is sent twice.
func TestTransfer_MFALapsingDuringTheRerunAfterAWaitPromptsAgain(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name      string
		stub      *lapseStub
		run       func(t *testing.T, ac *client.AlpaconClient) error
		wantLinks int32
		wantHits  int32
	}{
		{
			name: "upload status poll",
			// refused, probe, refused again on the re-run, probe, re-run
			stub: &lapseStub{method: http.MethodGet, path: "/api/webftp/uploads/t-1/status/", script: []int{stMFA, stOK, stMFA, stOK, stOK}},
			run: func(t *testing.T, ac *client.AlpaconClient) error {
				t.Helper()
				return UploadLocalFileAs(ac, writeTempFile(t, "a.txt"), "my-server", "/home/alice/a.txt", "", "", "")
			},
			wantLinks: 2,
			wantHits:  5,
		},
		{
			name: "download status poll",
			stub: &lapseStub{method: http.MethodGet, path: "/api/webftp/downloads/t-1/status/", script: []int{stMFA, stOK, stMFA, stOK, stOK}, blob: "content"},
			run: func(t *testing.T, ac *client.AlpaconClient) error {
				t.Helper()
				_, err := DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
				return err
			},
			wantLinks: 2,
			wantHits:  5,
		},
		{
			// A step that is itself the retried request (no probe) waits out a
			// lapse during its own retry inside the same wait: one link.
			name: "download fetch lapsing during the retry",
			stub: &lapseStub{method: http.MethodGet, path: "/blob", script: []int{stMFA, stMFA, stOK}, blob: "content"},
			run: func(t *testing.T, ac *client.AlpaconClient) error {
				t.Helper()
				_, err := DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
				return err
			},
			wantLinks: 1,
			wantHits:  3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ac := tc.stub.client()

			var err error
			synctest.Test(t, func(t *testing.T) {
				err = tc.run(t, ac)
			})

			require.NoError(t, err)
			assert.Equal(t, int32(1), tc.stub.creates.Load(), "the transfer must be created once")
			assert.Equal(t, tc.wantLinks, tc.stub.links.Load(), "one link per lapse of MFA")
			assert.Equal(t, tc.wantHits, tc.stub.hits.Load())
		})
	}
}

// A status poll that stays refused after its probe passes, wait after wait,
// ends with the refusal instead of issuing links without limit.
func TestPollTransfer_RefusedAfterEveryWaitEnds(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	var links, hits atomic.Int32
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			if r.URL.Path == "/api/auth0/mfa/" {
				links.Add(1)
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			}
			// Only the one-request probe passes; the full poll never does.
			if hits.Add(1)%2 == 0 {
				return http.StatusOK, `{"success": true, "message": "done"}`
			}
			return http.StatusForbidden, mfaRefusal
		}),
	}

	var err error
	synctest.Test(t, func(t *testing.T) {
		_, _, err = pollTransfer(ac, "srv-id", "upload", "t-1", 30*time.Second, time.Time{})
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "may already have completed")
	assert.Equal(t, int32(utils.MaxConsecutivePollFailures), links.Load(), "waits are bounded")
}
