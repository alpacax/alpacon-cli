package event

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
)

const sudoMFARefusal = `{"code": "auth_mfa_required", "source": "sudo"}`

// sudoVerifyStub answers a websh sudo MFA flow: the grant verification refuses
// with auth_mfa_required until it has been called refusals+1 times, and the MFA
// link and server lookup answer as the server does. Completion probes are counted
// and answered "not completed", so a listener waiting on them never finishes.
type sudoVerifyStub struct {
	refusals   int32
	verifies   atomic.Int32
	probes     atomic.Int32
	unexpected atomic.Int32
}

func (s *sudoVerifyStub) listener(t *testing.T) *SudoListener {
	t.Helper()
	// The fast path refreshes the token; an empty HOME gives it no stored
	// config to refresh from, so it fails without reaching the network.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ALPACON_NO_BROWSER", "1")

	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch r.URL.Path {
			case "/api/sudo/grants/grant-1/verify/":
				if s.verifies.Add(1) <= s.refusals {
					return http.StatusForbidden, sudoMFARefusal
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
				return http.StatusNotFound, `{}`
			}
		}),
	}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.pollInterval = 5 * time.Millisecond
	sl.pollTimeout = 2 * time.Second
	return sl
}

func grantEvent() sudoMFAEvent {
	var event sudoMFAEvent
	event.Payload.Type = "auth"
	event.Payload.Query = "mfa_request"
	event.Payload.SudoGrantID = "grant-1"
	return event
}

// After the browser opens, the verification itself is retried until the server
// accepts it: no completion endpoint is consulted, and the first accepted
// verification ends the flow.
func TestSudoListener_HandleSudoMFA_RetriesVerifyUntilMFAPasses(t *testing.T) {
	stub := &sudoVerifyStub{refusals: 3}
	sl := stub.listener(t)

	_, stderr := testutil.CaptureOutput(t, func() { sl.handleSudoMFA(grantEvent()) })

	assert.Contains(t, stderr, "Sudo MFA required")
	assert.NotContains(t, stderr, "timed out")
	assert.NotContains(t, stderr, "failed")
	assert.Equal(t, int32(4), stub.verifies.Load(),
		"the fast-path verify plus one retry per refusal, then stop on the one that passes")
	assert.Zero(t, stub.probes.Load(), "the wait must not ask a completion endpoint")
	assert.Zero(t, stub.unexpected.Load())
}

// A verification the server keeps refusing ends at the timeout, with the
// attempts bounded by it.
func TestSudoListener_HandleSudoMFA_StopsRetryingVerifyAtTheTimeout(t *testing.T) {
	stub := &sudoVerifyStub{refusals: 1 << 30}
	sl := stub.listener(t)
	sl.pollTimeout = 100 * time.Millisecond

	_, stderr := testutil.CaptureOutput(t, func() { sl.handleSudoMFA(grantEvent()) })

	assert.Contains(t, stderr, "MFA verification timed out")
	assert.Greater(t, stub.verifies.Load(), int32(1), "the refused verification must be retried")
	// 100ms at a 5ms interval, plus the fast-path verify.
	assert.LessOrEqual(t, stub.verifies.Load(), int32(1+20), "the timeout must bound the retries")
	assert.Zero(t, stub.probes.Load(), "the wait must not ask a completion endpoint")
}
