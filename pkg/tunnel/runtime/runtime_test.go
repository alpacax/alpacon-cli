package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	tunnelapi "github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xtaci/smux"
)

type mockSession struct {
	openStreamFn func() (*smux.Stream, error)
	closeFn      func() error
	closeChan    chan struct{}
}

func (m *mockSession) OpenStream() (*smux.Stream, error) {
	if m.openStreamFn != nil {
		return m.openStreamFn()
	}
	return nil, errors.New("open stream not implemented")
}

func (m *mockSession) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

func (m *mockSession) CloseChan() <-chan struct{} {
	if m.closeChan != nil {
		return m.closeChan
	}
	return make(chan struct{})
}

type mockListener struct {
	acceptFn func() (net.Conn, error)
	closeFn  func() error
}

func (m *mockListener) Accept() (net.Conn, error) {
	if m.acceptFn != nil {
		return m.acceptFn()
	}
	return nil, net.ErrClosed
}

func (m *mockListener) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

func (m *mockListener) Addr() net.Addr {
	return &net.TCPAddr{}
}

// tempNetError implements net.Error for testing retry logic.
type tempNetError struct {
	msg       string
	timeout   bool
	temporary bool
}

func (e *tempNetError) Error() string   { return e.msg }
func (e *tempNetError) Timeout() bool   { return e.timeout }
func (e *tempNetError) Temporary() bool { return e.temporary }

type writeCountingConn struct {
	net.Conn
	writes *atomic.Int32
}

func (c *writeCountingConn) Write(b []byte) (int, error) {
	c.writes.Add(1)
	return c.Conn.Write(b)
}

func TestShutdownRunsOnce(t *testing.T) {
	t.Parallel()
	cause := errors.New("shutdown cause")
	var listenerCloseCount atomic.Int32
	var sessionCloseCount atomic.Int32

	r := &Runtime{
		listener: &mockListener{
			closeFn: func() error {
				listenerCloseCount.Add(1)
				return nil
			},
		},
		session: &mockSession{
			closeFn: func() error {
				sessionCloseCount.Add(1)
				return nil
			},
		},
		done: make(chan struct{}),
	}

	const workers = 8
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			r.shutdown(cause)
		})
	}
	wg.Wait()

	select {
	case <-r.done:
	default:
		t.Fatal("expected done channel to be closed")
	}

	if got := listenerCloseCount.Load(); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := sessionCloseCount.Load(); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}

	if !errors.Is(r.Cause(), cause) {
		t.Fatalf("unexpected shutdown cause: %v", r.Cause())
	}
}

func TestAcceptConnectionsRetriesOnTemporaryError(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	const retryCount = 3

	r := &Runtime{
		listener: &mockListener{
			acceptFn: func() (net.Conn, error) {
				n := attempts.Add(1)
				if int(n) <= retryCount {
					return nil, &tempNetError{
						msg:       "temporary accept error",
						timeout:   true,
						temporary: true,
					}
				}
				// After retries, return a permanent closed error to exit the loop.
				return nil, net.ErrClosed
			},
		},
		session: &mockSession{},
		done:    make(chan struct{}),
	}

	r.acceptConnections()

	got := attempts.Load()
	if got <= int32(retryCount) {
		t.Fatalf("expected more than %d attempts, got %d", retryCount, got)
	}

	if err := r.Cause(); err != nil {
		t.Fatalf("unexpected shutdown cause: %v", err)
	}
}

func TestAcceptConnectionsTemporaryNonTimeoutErrorTriggersShutdown(t *testing.T) {
	t.Parallel()
	var listenerCloseCount atomic.Int32
	var sessionCloseCount atomic.Int32

	r := &Runtime{
		listener: &mockListener{
			acceptFn: func() (net.Conn, error) {
				return nil, &tempNetError{
					msg:       "temporary but not timeout",
					timeout:   false,
					temporary: true,
				}
			},
			closeFn: func() error {
				listenerCloseCount.Add(1)
				return nil
			},
		},
		session: &mockSession{
			closeFn: func() error {
				sessionCloseCount.Add(1)
				return nil
			},
		},
		done: make(chan struct{}),
	}

	r.acceptConnections()

	select {
	case <-r.done:
	default:
		t.Fatal("expected done channel to be closed")
	}

	if r.Cause() == nil {
		t.Fatal("expected shutdown cause, got nil")
	}

	if got := listenerCloseCount.Load(); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := sessionCloseCount.Load(); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}
}

func TestAcceptConnectionsErrorTriggersShutdown(t *testing.T) {
	t.Parallel()
	acceptErr := errors.New("accept failure")
	var listenerCloseCount atomic.Int32
	var sessionCloseCount atomic.Int32

	r := &Runtime{
		listener: &mockListener{
			acceptFn: func() (net.Conn, error) {
				return nil, acceptErr
			},
			closeFn: func() error {
				listenerCloseCount.Add(1)
				return nil
			},
		},
		session: &mockSession{
			closeFn: func() error {
				sessionCloseCount.Add(1)
				return nil
			},
		},
		done: make(chan struct{}),
	}

	r.acceptConnections()

	select {
	case <-r.done:
	default:
		t.Fatal("expected done channel to be closed")
	}

	if !errors.Is(r.Cause(), acceptErr) {
		t.Fatalf("shutdown cause = %v, want wrapped acceptErr", r.Cause())
	}

	if got := listenerCloseCount.Load(); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := sessionCloseCount.Load(); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}
}

func TestSessionCloseChanTriggersShutdown(t *testing.T) {
	t.Parallel()
	closeChan := make(chan struct{})
	var listenerCloseCount atomic.Int32
	var sessionCloseCount atomic.Int32

	r := &Runtime{
		listener: &mockListener{
			closeFn: func() error {
				listenerCloseCount.Add(1)
				return nil
			},
		},
		session: &mockSession{
			closeChan: closeChan,
			closeFn: func() error {
				sessionCloseCount.Add(1)
				return nil
			},
		},
		done: make(chan struct{}),
	}

	go func() {
		<-r.session.CloseChan()
		r.shutdown(fmt.Errorf("session closed by remote"))
	}()

	close(closeChan)

	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("expected done channel to be closed")
	}

	if cause := r.Cause(); cause == nil || cause.Error() != "session closed by remote" {
		t.Fatalf("shutdown cause = %v, want 'session closed by remote'", cause)
	}

	if got := listenerCloseCount.Load(); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := sessionCloseCount.Load(); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}
}

func TestParsePort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		value     string
		allowAuto bool
		wantErr   bool
	}{
		{name: "valid", value: "5432", allowAuto: false, wantErr: false},
		{name: "auto allowed", value: "0", allowAuto: true, wantErr: false},
		{name: "auto disallowed", value: "0", allowAuto: false, wantErr: true},
		{name: "too large", value: "70000", allowAuto: false, wantErr: true},
		{name: "non numeric", value: "abc", allowAuto: false, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePort(tt.value, tt.allowAuto)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidatePorts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		remotePort string
		localPort  string
		wantErr    string
	}{
		{name: "valid ports", remotePort: "5432", localPort: "9000"},
		{name: "empty local port means auto-assign", remotePort: "5432", localPort: ""},
		{name: "invalid remote port", remotePort: "invalid", localPort: "9000", wantErr: "invalid remote port: "},
		{name: "remote port zero", remotePort: "0", localPort: "", wantErr: "invalid remote port: "},
		{name: "invalid local port", remotePort: "5432", localPort: "70000", wantErr: "invalid local port: "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePorts(tt.remotePort, tt.localPort)

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestStartRefusesABlankServerNameAsAUsageError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, server string }{
		{name: "empty", server: ""},
		{name: "spaces", server: "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rt, err := Start(StartOptions{ServerName: tc.server, RemotePort: "5432"})

			require.ErrorIs(t, err, api.ErrBlankName)
			assert.True(t, utils.IsUsageError(err), "err: %v", err)
			assert.Nil(t, rt)
		})
	}
}

func TestStartRefusesANilClient(t *testing.T) {
	t.Parallel()

	rt, err := Start(StartOptions{ServerName: "my-db", RemotePort: "5432"})

	require.EqualError(t, err, "client is required")
	assert.Nil(t, rt)
}

func TestStartNamesTheServerWithoutTheSpacesAroundIt(t *testing.T) {
	t.Parallel()
	const serverID = "a1b2c3d4-1234-5678-abcd-000000000000"
	var wsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			if got := r.URL.Query().Get("name"); got != "my-db" {
				t.Errorf("lookup name = %q, want %q", got, "my-db")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "` + serverID + `", "name": "my-db"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/websh/tunnels/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": "tun-1", "websocket_url": "` + wsURL + `"}`))
		case r.URL.Path == "/ws/tunnel/channel-token/":
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.Method == http.MethodGet && r.URL.Path == "/api/websh/tunnels/tun-1/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": "tun-1", "open_status": "failed", "open_failure": {"result": "refused"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	wsURL = "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/tunnel/channel-token/"
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	rt, err := Start(StartOptions{Client: ac, ServerName: "  my-db  ", RemotePort: "5432"})

	assert.Nil(t, rt)
	require.EqualError(t, err, "failed to open tunnel on my-db: refused")
}

func TestExtractTCPPort(t *testing.T) {
	t.Parallel()
	t.Run("tcp address", func(t *testing.T) {
		port, err := extractTCPPort(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5432})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if port != 5432 {
			t.Fatalf("port = %d, want 5432", port)
		}
	})

	t.Run("non tcp address", func(t *testing.T) {
		_, err := extractTCPPort(&net.UnixAddr{Name: "/tmp/alpacon.sock", Net: "unix"})
		if err == nil {
			t.Fatal("expected error for non-TCP address")
		}
	})
}

func TestCheckReadyReturnsErrorWhenRuntimeAlreadyClosed(t *testing.T) {
	t.Parallel()
	r := &Runtime{
		done: make(chan struct{}),
	}
	close(r.done)

	closeErr := errors.New("session closed by remote")
	r.setCause(closeErr)

	err := r.CheckReady()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tunnel already closed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), closeErr.Error()) {
		t.Fatalf("error does not include cause: %v", err)
	}
}

func TestCheckReadyReturnsOpenStreamError(t *testing.T) {
	t.Parallel()
	r := &Runtime{
		done: make(chan struct{}),
		session: &mockSession{
			openStreamFn: func() (*smux.Stream, error) {
				return nil, errors.New("open failed")
			},
		},
		remotePort: "5432",
	}

	err := r.CheckReady()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to open readiness stream") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildTunnelMetadata(t *testing.T) {
	t.Parallel()
	metadataBytes, err := buildTunnelMetadata("5432")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	metadata := string(metadataBytes)
	if !strings.HasSuffix(metadata, "\n") {
		t.Fatalf("metadata should end with newline: %q", metadata)
	}
	if !strings.Contains(metadata, "\"remote_port\":\"5432\"") {
		t.Fatalf("unexpected metadata payload: %q", metadata)
	}
}

func TestTunnelDialerSendsAWholeSmuxFrameInOneWrite(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(ts.Close)
	var writes atomic.Int32
	maxFrameSize := config.GetSmuxConfig().MaxFrameSize
	dialer := *newTunnelDialer(maxFrameSize)
	dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &writeCountingConn{Conn: conn, writes: &writes}, nil
	}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	writes.Store(0)
	frame := make([]byte, smuxHeaderSize+maxFrameSize)

	err = conn.WriteMessage(websocket.BinaryMessage, frame)

	require.NoError(t, err)
	assert.Equal(t, int32(1), writes.Load())
}

func TestTunnelDialerKeepsTheDefaultDialerSettings(t *testing.T) {
	t.Parallel()

	dialer := newTunnelDialer(config.GetSmuxConfig().MaxFrameSize)

	assert.Equal(t, websocket.DefaultDialer.HandshakeTimeout, dialer.HandshakeTimeout)
	assert.NotNil(t, dialer.Proxy)
}

func TestExplainDialFailure(t *testing.T) {
	t.Parallel()
	failed := func(result string) *tunnelapi.TunnelSessionDetail {
		return &tunnelapi.TunnelSessionDetail{
			OpenStatus:  tunnelapi.OpenStatusFailed,
			OpenFailure: tunnelapi.OpenFailure{Result: result},
		}
	}
	pending := &tunnelapi.TunnelSessionDetail{OpenStatus: tunnelapi.OpenStatusPending}
	agentGone := &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}
	forbidden := &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden"}
	fallback := "failed to connect to proxy server: websocket: bad handshake"

	tests := []struct {
		name   string
		resp   *http.Response
		detail *tunnelapi.TunnelSessionDetail
		want   string
	}{
		{
			name:   "failed with a reason names the reason",
			resp:   agentGone,
			detail: failed("opentunnel: CPU usage 100.0% exceeds limit 90%"),
			want:   "failed to open tunnel on db-1: opentunnel: CPU usage 100.0% exceeds limit 90%",
		},
		{
			name:   "failed reason loses its terminal controls",
			detail: failed("\x1b[31mrefused\x1b[0m\u009b now"),
			want:   "failed to open tunnel on db-1: refused now",
		},
		{
			name:   "failed reason over several lines reads as one",
			detail: failed("refused:\r\n  CPU busy\n"),
			want:   "failed to open tunnel on db-1: refused: CPU busy",
		},
		{
			name:   "failed reason keeps tabs as word breaks",
			detail: failed("CPU\tbusy\x0bnow"),
			want:   "failed to open tunnel on db-1: CPU busy now",
		},
		{
			name:   "failed without a reason still says the open failed",
			resp:   forbidden,
			detail: failed(""),
			want:   "failed to open tunnel on db-1",
		},
		{
			name:   "failed with a blank reason still says the open failed",
			detail: failed(" \t\n "),
			want:   "failed to open tunnel on db-1",
		},
		{
			name:   "failed with only terminal controls still says the open failed",
			detail: failed("\x1b[0m"),
			want:   "failed to open tunnel on db-1",
		},
		{
			name:   "pending after the proxy gave up says the agent did not respond",
			resp:   agentGone,
			detail: pending,
			want:   "failed to open tunnel on db-1: the agent did not respond to the tunnel request",
		},
		{
			name:   "pending with another refusal keeps the proxy status",
			resp:   forbidden,
			detail: pending,
			want:   fallback + " (status 403 Forbidden)",
		},
		{
			name:   "pending without a handshake response keeps the dial error",
			detail: pending,
			want:   fallback,
		},
		{
			name:   "an unknown open status falls back",
			resp:   agentGone,
			detail: &tunnelapi.TunnelSessionDetail{OpenStatus: "closed"},
			want:   fallback + " (status 503 Service Unavailable)",
		},
		{
			name: "no detail falls back",
			resp: agentGone,
			want: fallback + " (status 503 Service Unavailable)",
		},
		{
			name:   "older server without open fields falls back",
			resp:   agentGone,
			detail: &tunnelapi.TunnelSessionDetail{},
			want:   fallback + " (status 503 Service Unavailable)",
		},
		{
			name:   "opened falls back",
			detail: &tunnelapi.TunnelSessionDetail{OpenStatus: tunnelapi.OpenStatusOpened},
			want:   fallback,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := explainDialFailure("db-1", websocket.ErrBadHandshake, tt.resp, tt.detail)

			assert.EqualError(t, err, tt.want)
		})
	}
}

func TestExplainDialFailureKeepsChannelTokenOutOfFallback(t *testing.T) {
	t.Parallel()
	dialErr := &url.Error{Op: "Get", URL: "wss://proxy.example.com/ws/tunnel/secret-token/", Err: websocket.ErrBadHandshake}

	err := explainDialFailure("db-1", dialErr, nil, nil)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-token")
	assert.Contains(t, err.Error(), "proxy.example.com")
	assert.ErrorIs(t, err, websocket.ErrBadHandshake)
}

func newRefusingTunnelServer(t *testing.T, proxyStatus int, detail http.HandlerFunc) (*client.AlpaconClient, *tunnelapi.TunnelSessionResponse, *atomic.Int32) {
	t.Helper()
	var lookups atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ws/tunnel/channel-token/":
			w.WriteHeader(proxyStatus)
		case r.Method == http.MethodGet && r.URL.Path == "/api/websh/tunnels/tun-1/":
			lookups.Add(1)
			detail(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	session := &tunnelapi.TunnelSessionResponse{
		ID:           "tun-1",
		WebsocketURL: "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/tunnel/channel-token/",
	}
	return ac, session, &lookups
}

func TestDialTunnelExplainsARefusedDialFromTheSessionDetail(t *testing.T) {
	t.Parallel()
	ac, session, lookups := newRefusingTunnelServer(t, http.StatusServiceUnavailable, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "tun-1", "open_status": "failed", "open_failure": {"result": "opentunnel: CPU usage 100.0% exceeds limit 90%"}}`))
	})

	conn, err := dialTunnel(ac, session, "db-1", config.GetSmuxConfig().MaxFrameSize, time.Second)

	assert.Nil(t, conn)
	require.EqualError(t, err, "failed to open tunnel on db-1: opentunnel: CPU usage 100.0% exceeds limit 90%")
	assert.Equal(t, int32(1), lookups.Load())
}

func TestDialTunnelPrintsTheLookupFailureOnlyWithTheDebugSwitch(t *testing.T) {
	for _, tc := range []struct {
		name, debug, want string
	}{
		{name: "debug_on", debug: "1", want: "could not read why the tunnel session did not open"},
		{name: "debug_off", debug: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(utils.DebugEnvVar, tc.debug)
			ac, session, _ := newRefusingTunnelServer(t, http.StatusServiceUnavailable, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			})

			var err error
			_, stderr := testutil.CaptureOutput(t, func() {
				_, err = dialTunnel(ac, session, "db-1", config.GetSmuxConfig().MaxFrameSize, time.Second)
			})

			require.EqualError(t, err, "failed to connect to proxy server: websocket: bad handshake (status 503 Service Unavailable)")
			if tc.want == "" {
				assert.Empty(t, stderr)
				return
			}
			assert.Contains(t, stderr, tc.want)
			assert.Contains(t, stderr, "permission denied", "the debug line names why the lookup failed")
		})
	}
}

func TestDialTunnelReportsTheDialErrorWhenTheLookupHangs(t *testing.T) {
	t.Parallel()
	ac, session, lookups := newRefusingTunnelServer(t, http.StatusServiceUnavailable, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second): // a lookup that ignores the timeout fails the elapsed check, not the run
		}
	})

	start := time.Now()
	conn, err := dialTunnel(ac, session, "db-1", config.GetSmuxConfig().MaxFrameSize, 50*time.Millisecond)

	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Nil(t, conn)
	require.EqualError(t, err, "failed to connect to proxy server: websocket: bad handshake (status 503 Service Unavailable)")
	assert.Equal(t, int32(1), lookups.Load())
}
