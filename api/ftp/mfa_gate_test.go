package ftp

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
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
			_, _, errs[i] = pollTransfer(ac, "srv-id", "upload", "t-1", 30*time.Second)
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
