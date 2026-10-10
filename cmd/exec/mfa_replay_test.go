package exec

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A command the server accepted must never be submitted again: after the MFA
// refusal the retry is accepted and the command completes, and a 503 on the
// chunk read that follows is a failure to read output, not a refused submission.
func TestRunCommandWithRetry_DoesNotResubmitAnAcceptedCommandWhenTheOutputReadFails(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())

	var submits, accepted atomic.Int32
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.URL.Path == "/api/events/sessions/":
				return http.StatusForbidden, `{"detail": "no event sessions"}`
			case r.URL.Path == "/api/events/commands/" && r.Method == http.MethodPost:
				if submits.Add(1) == 1 {
					return http.StatusForbidden, `{"code": "auth_mfa_required", "source": "server"}`
				}
				accepted.Add(1)
				return http.StatusCreated, `[{"id": "cmd-1", "server": {"id": "server-id"}}]`
			case r.URL.Path == "/api/events/commands/cmd-1/":
				return http.StatusOK, `{"id": "cmd-1", "status": "completed", "success": true, "result": ""}`
			case r.URL.Path == "/api/events/commands/cmd-1/chunks/":
				return http.StatusServiceUnavailable, `{"code": "api_search_unavailable"}`
			case r.URL.Path == "/api/auth0/mfa/":
				return http.StatusOK, `{"mfa_url": "https://example.com/mfa"}`
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "server-id"}]}`
			}
			return http.StatusNotFound, `{}`
		}),
	}

	var err error
	synctest.Test(t, func(t *testing.T) {
		err = RunCommandWithRetry(ac, "my-server", "echo hi", "", "", nil, "", "", io.Discard)
	})

	require.Error(t, err, "the unreadable output must reach the user")
	assert.Equal(t, int32(1), accepted.Load(), "an accepted command must not be submitted again")
}
