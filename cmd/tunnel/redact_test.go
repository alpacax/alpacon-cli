package tunnel

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A malformed tunnel URL quotes its path token in the dial error; the token must
// not print.
func TestExecuteTunnel_DialErrorOmitsThePathToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`))
		case "/api/websh/tunnels/":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"websocket_url": "wss://proxy.example.com/ws/tunnel/sid/cid/PATHTOKEN123/%zz"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)

	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(ts.URL, "my-workspace", "api-token", "", "", "", "", 0, false))
	saved := tunnelFlags
	t.Cleanup(func() { tunnelFlags = saved })
	tunnelFlags = tunnelFlagValues{localPort: "0", remotePort: "5432"}

	err := executeTunnel("my-server", nil)

	require.ErrorContains(t, err, "failed to connect to proxy server")
	assert.NotContains(t, err.Error(), "PATHTOKEN123")
}
