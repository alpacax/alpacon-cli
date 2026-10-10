package tunnel

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closedPortURL returns a ws:// URL on a local port nothing listens on, so a
// dial to it fails at once with a refused connection.
func closedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return "ws://" + addr + "/tunnel"
}

// The tunnel session exists once the server accepted its create. A dial to the
// proxy that fails after that is the tunnel's result: the MFA wait must not take
// the refused connection for a refused create and leave another session behind.
// Serial: it points HOME at a temp config and reassigns tunnelFlags.
func TestExecuteTunnel_MFAWaitDoesNotCreateAnotherSessionAfterAFailedDial(t *testing.T) {
	wsURL := closedPortURL(t)
	var creates atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`))
		case r.URL.Path == "/api/auth0/mfa/":
			_, _ = w.Write([]byte(`{"mfa_url": "https://example.com/mfa"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/websh/tunnels/":
			if creates.Add(1) == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"code": "auth_mfa_required", "source": "tunnel"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"websocket_url": "` + wsURL + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("ALPACON_NO_BROWSER", "1")
	require.NoError(t, config.CreateConfig(ts.URL, "my-workspace", "api-token", "", "", "", "", 0, false))

	saved := tunnelFlags
	t.Cleanup(func() { tunnelFlags = saved })
	tunnelFlags = tunnelFlagValues{localPort: "0", remotePort: "5432"}

	err := executeTunnel("my-server", nil)

	require.ErrorContains(t, err, "failed to connect to proxy server")
	assert.Equal(t, int32(1), creates.Load()-1, "one accepted tunnel session")
}
