package tunnel_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTunnelBodyCapture holds the POST body fields of /api/websh/tunnels/; mu
// guards them against the handler goroutine.
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

// newCreateTunnelBodyCaptureServer answers the server lookup by name with one
// server and captures the tunnel create body.
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
	t.Parallel()
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
	t.Parallel()
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

func newTunnelDetailClient(t *testing.T, handler http.HandlerFunc) *client.AlpaconClient {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/websh/tunnels/tun-1/" {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(ts.Close)
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
}

func answerTunnelDetail(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestGetTunnelSessionReadsFailedOpen(t *testing.T) {
	t.Parallel()
	ac := newTunnelDetailClient(t, answerTunnelDetail(http.StatusOK, `{
		"id": "tun-1",
		"open_status": "failed",
		"open_failure": {
			"command": "4f1c2a8e-0000-4000-8000-000000000001",
			"status": "error",
			"error_phase": "connect",
			"exit_code": 1,
			"result": "connection refused"
		}
	}`))

	detail, err := tunnel.GetTunnelSession(context.Background(), ac, "tun-1")

	require.NoError(t, err)
	assert.Equal(t, &tunnel.TunnelSessionDetail{
		OpenStatus:  tunnel.OpenStatusFailed,
		OpenFailure: tunnel.OpenFailure{Result: "connection refused"},
	}, detail)
}

func TestGetTunnelSessionFromOlderServerLeavesOpenFieldsEmpty(t *testing.T) {
	t.Parallel()
	ac := newTunnelDetailClient(t, answerTunnelDetail(http.StatusOK, `{"id": "tun-1", "target_port": 5432}`))

	detail, err := tunnel.GetTunnelSession(context.Background(), ac, "tun-1")

	require.NoError(t, err)
	assert.Equal(t, &tunnel.TunnelSessionDetail{}, detail)
}

func TestGetTunnelSessionReadsNullOpenFieldsAsEmpty(t *testing.T) {
	t.Parallel()
	ac := newTunnelDetailClient(t, answerTunnelDetail(http.StatusOK, `{"id": "tun-1", "open_status": null, "open_failure": null}`))

	detail, err := tunnel.GetTunnelSession(context.Background(), ac, "tun-1")

	require.NoError(t, err)
	assert.Equal(t, &tunnel.TunnelSessionDetail{}, detail)
}

func TestGetTunnelSessionReturnsErrorOnNon2xx(t *testing.T) {
	t.Parallel()
	ac := newTunnelDetailClient(t, answerTunnelDetail(http.StatusForbidden, `{"code": "permission_denied"}`))

	detail, err := tunnel.GetTunnelSession(context.Background(), ac, "tun-1")

	require.Error(t, err)
	assert.Equal(t, http.StatusForbidden, utils.HTTPStatusCode(err))
	assert.Contains(t, err.Error(), "failed to get tunnel session")
	assert.Nil(t, detail)
}

func TestGetTunnelSessionReturnsErrorOnMalformedBody(t *testing.T) {
	t.Parallel()
	ac := newTunnelDetailClient(t, answerTunnelDetail(http.StatusOK, `{"open_status":`))

	detail, err := tunnel.GetTunnelSession(context.Background(), ac, "tun-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse tunnel session detail")
	assert.Nil(t, detail)
}

func TestGetTunnelSessionGivesUpWhenTheContextExpires(t *testing.T) {
	t.Parallel()
	ac := newTunnelDetailClient(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second): // a request sent without ctx fails the elapsed check, not the run
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	detail, err := tunnel.GetTunnelSession(ctx, ac, "tun-1")

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Nil(t, detail)
}

func TestGetTunnelSessionRefusesUnusableID(t *testing.T) {
	t.Parallel()
	ac := &client.AlpaconClient{}

	detail, err := tunnel.GetTunnelSession(context.Background(), ac, "../servers")

	require.ErrorIs(t, err, api.ErrUnusableID)
	assert.Nil(t, detail)
}
