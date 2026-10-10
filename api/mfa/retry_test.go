package mfa

import (
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	gatedPath      = "/api/gated/"
	mfaRefusalBody = `{"code": "auth_mfa_required", "source": "server"}`
)

// mfaGateStub answers the MFA link, the server lookup, and a gated endpoint that
// refuses with auth_mfa_required until it has been called refusals+1 times.
// Completion probes are counted and answered "not completed", so a caller that
// waits on them never finishes.
type mfaGateStub struct {
	refusals    int32
	gatedCalls  atomic.Int32
	probes      atomic.Int32
	unexpected  atomic.Int32
	lastUnknown atomic.Value
}

func (s *mfaGateStub) client() *client.AlpaconClient {
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case gatedPath:
				if s.gatedCalls.Add(1) <= s.refusals {
					return http.StatusForbidden, mfaRefusalBody
				}
				return http.StatusOK, `{}`
			case "/api/auth0/mfa/":
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			case "/api/auth0/mfa/completion/":
				s.probes.Add(1)
				return http.StatusOK, `{"completed": false}`
			case "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "server-id"}]}`
			default:
				s.unexpected.Add(1)
				s.lastUnknown.Store(r.URL.Path)
				return http.StatusNotFound, `{}`
			}
		}),
	}
}

type callbacksFactory func(*client.AlpaconClient, func() error) utils.ErrorHandlerCallbacks

var callbackSets = []struct {
	name string
	make callbacksFactory
}{
	{"ErrorCallbacks", ErrorCallbacks},
	{"WorkspaceErrorCallbacks", WorkspaceErrorCallbacks},
}

// After the MFA prompt the refused request itself is retried until the server
// lets it through: no completion endpoint is consulted, and the wait ends on the
// first attempt that passes.
func TestMFACallbacks_RetryTheRefusedRequestUntilItPasses(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")

	for _, tc := range callbackSets {
		t.Run(tc.name, func(t *testing.T) {
			stub := &mfaGateStub{refusals: 3}
			ac := stub.client()
			gated := func() error {
				_, err := ac.SendGetRequest(gatedPath)
				return utils.MarkSubmission(err)
			}

			var err error
			synctest.Test(t, func(t *testing.T) {
				first := gated()
				require.Error(t, first)
				err = utils.HandleCommonErrors(first, "my-server", tc.make(ac, gated))
			})

			require.NoError(t, err)
			assert.Equal(t, int32(4), stub.gatedCalls.Load(),
				"the first call plus one retry per refusal, then stop on the one that passes")
			assert.Zero(t, stub.probes.Load(), "the wait must not ask a completion endpoint")
			assert.Zero(t, stub.unexpected.Load(), "unexpected request to %v", stub.lastUnknown.Load())
		})
	}
}

// A request the server keeps refusing ends at the timeout, with the attempts
// bounded by it and still no completion probe.
func TestMFACallbacks_StopRetryingAtTheTimeout(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")

	for _, tc := range callbackSets {
		t.Run(tc.name, func(t *testing.T) {
			stub := &mfaGateStub{refusals: 1 << 30}
			ac := stub.client()
			gated := func() error {
				_, err := ac.SendGetRequest(gatedPath)
				return utils.MarkSubmission(err)
			}

			var err error
			synctest.Test(t, func(t *testing.T) {
				first := gated()
				require.Error(t, first)
				err = utils.HandleCommonErrors(first, "my-server", tc.make(ac, gated))
			})

			require.ErrorContains(t, err, "MFA authentication timed out after 3m0s")
			// One attempt a second over three minutes, plus the first call.
			assert.Greater(t, stub.gatedCalls.Load(), int32(1), "the refused request must be retried")
			assert.LessOrEqual(t, stub.gatedCalls.Load(), int32(1+181), "the timeout must bound the retries")
			assert.Zero(t, stub.probes.Load(), "the wait must not ask a completion endpoint")
		})
	}
}
