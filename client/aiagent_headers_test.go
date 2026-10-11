package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setAgentEnv clears every variable agent detection reads, then sets env, so
// the result does not depend on the shell the tests run in.
func setAgentEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range aiagent.Variables() {
		t.Setenv(name, "")
	}
	for name, value := range env {
		t.Setenv(name, value)
	}
}

// headerRecorder is a server that keeps the headers of the last HTTP request
// and of the last WebSocket handshake it received.
type headerRecorder struct {
	mu        sync.Mutex
	http      http.Header
	handshake http.Header
}

func newHeaderRecorderServer(t *testing.T) (*httptest.Server, *headerRecorder) {
	t.Helper()
	rec := &headerRecorder{}
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			rec.mu.Lock()
			rec.handshake = r.Header.Clone()
			rec.mu.Unlock()
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		rec.mu.Lock()
		rec.http = r.Header.Clone()
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	return ts, rec
}

func (rec *headerRecorder) headers() (httpHeader, handshake http.Header) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.http, rec.handshake
}

func dialWebsocket(t *testing.T, ac *AlpaconClient, serverURL string) {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(serverURL, "http"), ac.SetWebsocketHeader())
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	_ = conn.Close()
}

func TestDeclaredAgentHeaders_SentOnHTTPRequestAndWebsocketHandshake(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantName    string // empty: the header must be absent
		wantVersion string // empty: the header must be absent
	}{
		{
			name:        "AI_AGENT with a version",
			env:         map[string]string{aiagent.EnvAIAgent: "claude-code_2-1-120_agent"},
			wantName:    "claude-code",
			wantVersion: "2.1.120",
		},
		{
			name:     "marker without a version",
			env:      map[string]string{"CODEX_THREAD_ID": "thread-1"},
			wantName: "codex",
		},
		{
			name:        "over-long values are cut to the server's limits",
			env:         map[string]string{aiagent.EnvAIAgent: strings.Repeat("a", 70) + "/" + strings.Repeat("9", 40)},
			wantName:    strings.Repeat("a", 64),
			wantVersion: strings.Repeat("9", 32),
		},
		{
			name: "no markers",
			env:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setAgentEnv(t, tt.env)
			ts, rec := newHeaderRecorderServer(t)
			ac := newTestClient(ts.URL)

			_, err := ac.SendGetRequest("/api/test/")
			require.NoError(t, err)
			dialWebsocket(t, ac, ts.URL)

			httpHeader, handshake := rec.headers()
			require.NotNil(t, httpHeader, "the HTTP request did not reach the server")
			require.NotNil(t, handshake, "the WebSocket handshake did not reach the server")
			for surface, h := range map[string]http.Header{"HTTP request": httpHeader, "WebSocket handshake": handshake} {
				assertDeclaredHeader(t, surface, h, aiagent.NameHeader, tt.wantName)
				assertDeclaredHeader(t, surface, h, aiagent.VersionHeader, tt.wantVersion)
			}
		})
	}
}

func TestSendRawRequest_CallerVersionDroppedWhenAgentVersionUnknown(t *testing.T) {
	setAgentEnv(t, map[string]string{"GEMINI_CLI": "1"})
	ts, rec := newHeaderRecorderServer(t)
	defer ts.Close()
	ac := newTestClient(ts.URL)

	header := http.Header{}
	header.Set(aiagent.VersionHeader, "9.9.9")
	resp, err := ac.SendRawRequest(http.MethodGet, "/api/test/", nil, header)
	require.NoError(t, err)
	require.NotNil(t, resp)

	httpHeader, _ := rec.headers()
	require.NotNil(t, httpHeader)
	assertDeclaredHeader(t, "raw request", httpHeader, aiagent.NameHeader, "gemini-cli")
	assertDeclaredHeader(t, "raw request", httpHeader, aiagent.VersionHeader, "")
}

func TestSendRawRequest_CallerHeaderDoesNotReplaceDeclaredAgent(t *testing.T) {
	setAgentEnv(t, map[string]string{"GEMINI_CLI": "1"})
	ts, rec := newHeaderRecorderServer(t)
	ac := newTestClient(ts.URL)

	header := http.Header{}
	header.Set(aiagent.NameHeader, "someone-else")
	resp, err := ac.SendRawRequest(http.MethodGet, "/api/test/", nil, header)
	require.NoError(t, err)
	require.NotNil(t, resp)

	httpHeader, _ := rec.headers()
	require.NotNil(t, httpHeader)
	assertDeclaredHeader(t, "raw request", httpHeader, aiagent.NameHeader, "gemini-cli")
}

// assertDeclaredHeader checks that h carries exactly one value want for name,
// or no value at all when want is empty.
func assertDeclaredHeader(t *testing.T, surface string, h http.Header, name, want string) {
	t.Helper()
	if want == "" {
		assert.NotContains(t, h, name, "%s: %s must be absent", surface, name)
		return
	}
	assert.Equal(t, []string{want}, h.Values(name), "%s: %s", surface, name)
}
