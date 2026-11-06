package tunnel

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// A malformed tunnel URL quotes its path token in the dial error; the token must
// not print.
func TestTunnelCommand_DialErrorOmitsThePathToken(t *testing.T) {
	t.Parallel()
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

	code, stderr := runTunnelCommand(t, stubClient(ts), tunnelArgs("my-server")...)

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.Contains(t, stderr, "failed to connect to proxy server")
	assert.NotContains(t, stderr, "PATHTOKEN123")
}
