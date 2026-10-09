package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/config"
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
	var listenerCloseCount int32
	var sessionCloseCount int32

	r := &Runtime{
		listener: &mockListener{
			closeFn: func() error {
				atomic.AddInt32(&listenerCloseCount, 1)
				return nil
			},
		},
		session: &mockSession{
			closeFn: func() error {
				atomic.AddInt32(&sessionCloseCount, 1)
				return nil
			},
		},
		done: make(chan struct{}),
	}

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			r.shutdown(cause)
		}()
	}
	wg.Wait()

	select {
	case <-r.done:
	default:
		t.Fatal("expected done channel to be closed")
	}

	if got := atomic.LoadInt32(&listenerCloseCount); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sessionCloseCount); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}

	if !errors.Is(r.Cause(), cause) {
		t.Fatalf("unexpected shutdown cause: %v", r.Cause())
	}
}

func TestAcceptConnectionsRetriesOnTemporaryError(t *testing.T) {
	t.Parallel()
	var attempts int32
	const retryCount = 3

	r := &Runtime{
		listener: &mockListener{
			acceptFn: func() (net.Conn, error) {
				n := atomic.AddInt32(&attempts, 1)
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

	got := atomic.LoadInt32(&attempts)
	if got <= int32(retryCount) {
		t.Fatalf("expected more than %d attempts, got %d", retryCount, got)
	}

	if err := r.Cause(); err != nil {
		t.Fatalf("unexpected shutdown cause: %v", err)
	}
}

func TestAcceptConnectionsTemporaryNonTimeoutErrorTriggersShutdown(t *testing.T) {
	t.Parallel()
	var listenerCloseCount int32
	var sessionCloseCount int32

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
				atomic.AddInt32(&listenerCloseCount, 1)
				return nil
			},
		},
		session: &mockSession{
			closeFn: func() error {
				atomic.AddInt32(&sessionCloseCount, 1)
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

	if got := atomic.LoadInt32(&listenerCloseCount); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sessionCloseCount); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}
}

func TestAcceptConnectionsErrorTriggersShutdown(t *testing.T) {
	t.Parallel()
	acceptErr := errors.New("accept failure")
	var listenerCloseCount int32
	var sessionCloseCount int32

	r := &Runtime{
		listener: &mockListener{
			acceptFn: func() (net.Conn, error) {
				return nil, acceptErr
			},
			closeFn: func() error {
				atomic.AddInt32(&listenerCloseCount, 1)
				return nil
			},
		},
		session: &mockSession{
			closeFn: func() error {
				atomic.AddInt32(&sessionCloseCount, 1)
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

	if got := atomic.LoadInt32(&listenerCloseCount); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sessionCloseCount); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}
}

func TestSessionCloseChanTriggersShutdown(t *testing.T) {
	t.Parallel()
	closeChan := make(chan struct{})
	var listenerCloseCount int32
	var sessionCloseCount int32

	r := &Runtime{
		listener: &mockListener{
			closeFn: func() error {
				atomic.AddInt32(&listenerCloseCount, 1)
				return nil
			},
		},
		session: &mockSession{
			closeChan: closeChan,
			closeFn: func() error {
				atomic.AddInt32(&sessionCloseCount, 1)
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

	if got := atomic.LoadInt32(&listenerCloseCount); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sessionCloseCount); got != 1 {
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
