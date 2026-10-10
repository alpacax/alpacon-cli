package websh

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

// With --share, the session exists once the server accepted its create. A share
// request that fails after that, even with a 503, is the command's result: the
// MFA wait must not take it for a refused create and open another session.
// Serial: pinTerminalSize reassigns a package-level seam.
func TestCreateWebshSession_MFAWaitDoesNotOpenAnotherSessionAfterAFailedShare(t *testing.T) {
	pinTerminalSize(t, 24, 80)

	var creates, shares atomic.Int32
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
			case r.Method == http.MethodPost && r.URL.Path == sessionsBaseURL:
				if creates.Add(1) == 1 {
					return http.StatusForbidden, `{"code": "auth_mfa_required", "source": "websh"}`
				}
				return http.StatusCreated, `{"id": "sess-1"}`
			case r.Method == http.MethodPost && r.URL.Path == sessionsBaseURL+"sess-1/share/":
				shares.Add(1)
				return http.StatusServiceUnavailable, `{"detail": "unavailable"}`
			default:
				return http.StatusNotFound, `{}`
			}
		}),
	}
	create := func() error {
		_, err := CreateWebshSession(ac, "my-server", "", "", true, false, "")
		return err
	}

	var err error
	synctest.Test(t, func(t *testing.T) {
		first := create()
		code, _ := utils.ParseErrorResponse(first)
		require.Equal(t, utils.AuthMFARequired, code, "got %v", first)
		err = utils.HandleCommonErrors(first, "my-server", utils.ErrorHandlerCallbacks{
			OnMFARequired:  func(string) error { return nil },
			RetryOperation: create,
		})
	})

	require.Error(t, err)
	assert.Equal(t, int32(1), creates.Load()-1, "one accepted session create")
	assert.Equal(t, int32(1), shares.Load())
}
