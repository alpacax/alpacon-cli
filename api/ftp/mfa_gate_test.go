package ftp

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gatePolls = 8

// gateStub serves gatePolls concurrent status polls that are all refused for
// MFA at once. afterMFA is what a request answers once the MFA link was
// issued: the probe of the wait, and any poll after it.
type gateStub struct {
	links     atomic.Int32
	arrived   atomic.Int32
	statusHit atomic.Int32
	release   chan struct{}
	afterMFA  func() (int, string)
}

func newGateStub(afterMFA func() (int, string)) *gateStub {
	return &gateStub{release: make(chan struct{}), afterMFA: afterMFA}
}

func (s *gateStub) client() *client.AlpaconClient {
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case "/api/auth0/mfa/":
				s.links.Add(1)
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			default:
				s.statusHit.Add(1)
				if s.links.Load() == 0 {
					// Hold every first poll until all of them have been refused
					// together, so none of them can start after the wait ended.
					if s.arrived.Add(1) == gatePolls {
						close(s.release)
					}
					<-s.release
					return http.StatusForbidden, mfaRefusal
				}
				return s.afterMFA()
			}
		}),
	}
}

func runGatePolls(t *testing.T, ac *client.AlpaconClient) []error {
	t.Helper()
	errs := make([]error, gatePolls)
	var wg sync.WaitGroup
	for i := range gatePolls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = pollTransfer(ac, "srv-id", "upload", "t-1", 30*time.Second, time.Time{})
		}()
	}
	wg.Wait()
	return errs
}

// Polls refused together share one MFA prompt, and when that wait fails each of
// them returns the same error instead of opening a link and waiting again.
func TestPollTransfer_SiblingsShareAFailedMFAWait(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	stub := newGateStub(func() (int, string) { return http.StatusNotFound, `{"detail": "gone"}` })

	errs := runGatePolls(t, stub.client())

	assert.Equal(t, int32(1), stub.links.Load(), "one MFA link for all polls")
	for i, err := range errs {
		require.Error(t, err, "poll %d", i)
		assert.Contains(t, err.Error(), "may already have completed", "poll %d", i)
	}
}

// When the wait succeeds, the polls share its one link and each completes.
func TestPollTransfer_SiblingsShareASuccessfulMFAWait(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	stub := newGateStub(func() (int, string) { return http.StatusOK, `{"success": true, "message": "done"}` })

	errs := runGatePolls(t, stub.client())

	assert.Equal(t, int32(1), stub.links.Load(), "one MFA link for all polls")
	for i, err := range errs {
		assert.NoError(t, err, "poll %d", i)
	}
}

const bulkFiles = 20

// bulkGateStub serves a bulk upload of bulkFiles files whose status polls are
// refused for MFA. The polls run bulkPollConcurrency at a time, so the first
// batch is held until it is refused together and the rest queue behind it.
type bulkGateStub struct {
	links    atomic.Int32
	arrived  atomic.Int32
	release  chan struct{}
	afterMFA func() (int, string)
}

func (s *bulkGateStub) client() *client.AlpaconClient {
	slots := `[`
	for i := range bulkFiles {
		if i > 0 {
			slots += `,`
		}
		slots += fmt.Sprintf(`{"id": "t-%d"}`, i)
	}
	slots += `]`
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.URL.Path == "/api/auth0/mfa/":
				s.links.Add(1)
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
			case r.Method == http.MethodPost && r.URL.Path == uploadBulkAPIURL:
				return http.StatusCreated, slots
			case r.Method == http.MethodPost && r.URL.Path == uploadBulkTriggerURL:
				return http.StatusOK, `{}`
			case strings.HasSuffix(r.URL.Path, "/status/"):
				if s.links.Load() == 0 {
					if s.arrived.Add(1) == bulkPollConcurrency {
						close(s.release)
					}
					<-s.release
					return http.StatusForbidden, mfaRefusal
				}
				return s.afterMFA()
			}
			return http.StatusNotFound, `{}`
		}),
	}
}

func runBulkUpload(t *testing.T, stub *bulkGateStub) error {
	t.Helper()
	var files []string
	for i := range bulkFiles {
		files = append(files, writeTempFile(t, fmt.Sprintf("f%d.txt", i)))
	}
	return UploadFile(stub.client(), files, "my-server:/home/alice/", "", "", false, "")
}

// A bulk command's polls queue behind the first batch, so they start after a
// failed wait ended. They still belong to the same command: one link, and every
// file reports the error.
func TestBulkUpload_QueuedPollsShareAFailedMFAWait(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	// The wait's probe is the first read after the link: it fails, ending the
	// wait. The server keeps refusing every later read for MFA.
	var afterLink atomic.Int32
	stub := &bulkGateStub{release: make(chan struct{}), afterMFA: func() (int, string) {
		if afterLink.Add(1) == 1 {
			return http.StatusNotFound, `{"detail": "gone"}`
		}
		return http.StatusForbidden, mfaRefusal
	}}

	err := runBulkUpload(t, stub)

	require.Error(t, err)
	assert.Equal(t, int32(1), stub.links.Load(), "one MFA link for the whole command")
	assert.Equal(t, bulkFiles, strings.Count(err.Error(), "may already have completed"), "every file reports it: %v", err)
}

func TestBulkUpload_QueuedPollsShareASuccessfulMFAWait(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	stub := &bulkGateStub{release: make(chan struct{}), afterMFA: func() (int, string) { return http.StatusOK, `{"success": true, "message": "done"}` }}

	require.NoError(t, runBulkUpload(t, stub))
	assert.Equal(t, int32(1), stub.links.Load(), "one MFA link for the whole command")
}

// The probe tolerates a transient status read the way the full poll does.
func TestPollTransfer_ProbeRidesThroughATransientError(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	var links, statusHits atomic.Int32
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			if r.URL.Path == "/api/auth0/mfa/" {
				links.Add(1)
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			}
			switch statusHits.Add(1) {
			case 1:
				return http.StatusForbidden, mfaRefusal
			case 2: // the probe's first read
				return http.StatusBadGateway, `{"detail": "bad gateway"}`
			default:
				return http.StatusOK, `{"success": true, "message": "done"}`
			}
		}),
	}

	var err error
	synctest.Test(t, func(t *testing.T) {
		_, _, err = pollTransfer(ac, "srv-id", "upload", "t-1", 30*time.Second, time.Time{})
	})

	require.NoError(t, err)
	assert.Equal(t, int32(1), links.Load())
}
