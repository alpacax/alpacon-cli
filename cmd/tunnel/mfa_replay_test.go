package tunnel

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	tunnelruntime "github.com/alpacax/alpacon-cli/pkg/tunnel/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tunnel session the server created must not be created again: after the MFA
// refusal the retry is accepted, and a failure to reach the proxy afterwards is
// not a refused creation.
func TestHandleTunnelStartError_DoesNotRecreateAnAcceptedSessionWhenTheProxyIsUnreachable(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())

	var creates, accepted atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/websh/tunnels/":
			if creates.Add(1) == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"code": "auth_mfa_required", "source": "server"}`))
				return
			}
			accepted.Add(1)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": "t-1", "websocket_url": "ws://127.0.0.1:1/"}`))
		case "/api/auth0/mfa/":
			_, _ = w.Write([]byte(`{"mfa_url": "https://example.com/mfa"}`))
		case "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "server-id"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	require.NoError(t, config.CreateConfig(srv.URL, "ws", "token", "", "", "", "", 0, false))

	opts := tunnelruntime.StartOptions{ServerName: "my-server", RemotePort: "22"}
	_, err := tunnelruntime.Start(opts)
	require.Error(t, err)

	err = handleTunnelStartError(err, "my-server", func() error {
		_, err := tunnelruntime.Start(opts)
		return err
	})

	require.Error(t, err, "the unreachable proxy must reach the user")
	assert.Equal(t, int32(1), accepted.Load(), "an accepted session must not be created again")
}
