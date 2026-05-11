package tunnel_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTunnelBodyCapture holds the captured POST body fields for the
// /api/websh/tunnels/ request. Access is guarded by mu because the
// test server handler runs on a separate goroutine from the test body.
type createTunnelBodyCapture struct {
	mu                sync.Mutex
	hadWorkSessionKey bool
	workSession       string
	postSeen          bool
}

func (c *createTunnelBodyCapture) record(payload map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := payload["work_session"]
	c.hadWorkSessionKey = ok
	if ok {
		c.workSession, _ = v.(string)
	}
	c.postSeen = true
}

func (c *createTunnelBodyCapture) snapshot() (hadKey bool, ws string, seen bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hadWorkSessionKey, c.workSession, c.postSeen
}

// newCreateTunnelBodyCaptureServer returns a test server that:
//   - responds to GET /api/servers/servers/ (lookup by name) with a 1-item list
//   - captures the POST body for /api/websh/tunnels/ and returns a minimal
//     TunnelSessionResponse so CreateTunnelSession returns nil error.
func newCreateTunnelBodyCaptureServer(t *testing.T, capture *createTunnelBodyCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/servers/servers/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.ListResponse[map[string]any]{
				Count: 1,
				Results: []map[string]any{
					{"id": "srv-1", "name": "server-x"},
				},
			})
			return
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/websh/tunnels/") {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			capture.record(payload)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            "tnl-1",
				"websocket_url": "ws://localhost/ws",
				"target_port":   5432,
			})
			return
		}
		http.NotFound(w, r)
	}))
}

func TestCreateTunnelSession_BodyIncludesWorkSession_WhenSet(t *testing.T) {
	var capture createTunnelBodyCapture
	ts := newCreateTunnelBodyCaptureServer(t, &capture)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	resp, err := tunnel.CreateTunnelSession(ac, "server-x", "alice", "ops", 5432, "ses-abc")
	require.NoError(t, err)
	require.NotNil(t, resp)

	hadKey, ws, seen := capture.snapshot()
	require.True(t, seen, "POST must reach the test server")
	require.True(t, hadKey, "body must contain work_session field when ID is set")
	assert.Equal(t, "ses-abc", ws)
}

func TestCreateTunnelSession_BodyOmitsWorkSession_WhenEmpty(t *testing.T) {
	var capture createTunnelBodyCapture
	ts := newCreateTunnelBodyCaptureServer(t, &capture)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	resp, err := tunnel.CreateTunnelSession(ac, "server-x", "alice", "ops", 5432, "")
	require.NoError(t, err)
	require.NotNil(t, resp)

	hadKey, _, seen := capture.snapshot()
	require.True(t, seen, "POST must reach the test server")
	assert.False(t, hadKey, "body must omit work_session field when ID is empty")
}
