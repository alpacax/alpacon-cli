package tunnel

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestShutdownTunnelRunsOnce(t *testing.T) {
	cause := errors.New("shutdown cause")
	var listenerCloseCount int32
	var sessionCloseCount int32

	ctx := &tunnelContext{
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
		done:        make(chan struct{}),
		shutdownErr: make(chan error, 1),
	}

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			shutdownTunnel(ctx, cause)
		}()
	}
	wg.Wait()

	select {
	case <-ctx.done:
	default:
		t.Fatal("expected done channel to be closed")
	}

	if got := atomic.LoadInt32(&listenerCloseCount); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sessionCloseCount); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}

	select {
	case err := <-ctx.shutdownErr:
		if !errors.Is(err, cause) {
			t.Fatalf("unexpected shutdown cause: %v", err)
		}
	default:
		t.Fatal("expected shutdown cause in shutdownErr channel")
	}
}

// tempNetError implements net.Error with Timeout() == true for testing retry logic.
type tempNetError struct{ msg string }

func (e *tempNetError) Error() string   { return e.msg }
func (e *tempNetError) Timeout() bool   { return true }
func (e *tempNetError) Temporary() bool { return true }

func TestAcceptConnectionsRetriesOnTemporaryError(t *testing.T) {
	var attempts int32
	const retryCount = 3

	ctx := &tunnelContext{
		listener: &mockListener{
			acceptFn: func() (net.Conn, error) {
				n := atomic.AddInt32(&attempts, 1)
				if int(n) <= retryCount {
					return nil, &tempNetError{msg: "temporary accept error"}
				}
				// After retries, return a permanent error to exit the loop
				return nil, net.ErrClosed
			},
		},
		session:     &mockSession{},
		done:        make(chan struct{}),
		shutdownErr: make(chan error, 1),
	}

	acceptConnections(ctx)

	got := atomic.LoadInt32(&attempts)
	if got <= int32(retryCount) {
		t.Fatalf("expected more than %d attempts, got %d", retryCount, got)
	}

	// shutdownTunnel should NOT have been called (exited via net.ErrClosed path)
	select {
	case err := <-ctx.shutdownErr:
		t.Fatalf("unexpected shutdownErr: %v", err)
	default:
	}
}

func TestAcceptConnectionsErrorTriggersShutdown(t *testing.T) {
	acceptErr := errors.New("accept failure")
	var listenerCloseCount int32
	var sessionCloseCount int32

	ctx := &tunnelContext{
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
		done:        make(chan struct{}),
		shutdownErr: make(chan error, 1),
	}

	acceptConnections(ctx)

	select {
	case <-ctx.done:
	default:
		t.Fatal("expected done channel to be closed")
	}

	select {
	case err := <-ctx.shutdownErr:
		if !errors.Is(err, acceptErr) {
			t.Fatalf("shutdownErr = %v, want wrapped acceptErr", err)
		}
	default:
		t.Fatal("expected shutdown cause in shutdownErr channel")
	}

	if got := atomic.LoadInt32(&listenerCloseCount); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sessionCloseCount); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}
}

func TestSessionCloseChanTriggersShutdown(t *testing.T) {
	closeChan := make(chan struct{})
	var listenerCloseCount int32
	var sessionCloseCount int32

	ctx := &tunnelContext{
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
		done:        make(chan struct{}),
		shutdownErr: make(chan error, 1),
	}

	go func() {
		<-ctx.session.CloseChan()
		shutdownTunnel(ctx, fmt.Errorf("session closed by remote"))
	}()

	close(closeChan)

	select {
	case <-ctx.done:
	case <-time.After(time.Second):
		t.Fatal("expected done channel to be closed")
	}

	select {
	case err := <-ctx.shutdownErr:
		if err == nil || err.Error() != "session closed by remote" {
			t.Fatalf("shutdownErr = %v, want 'session closed by remote'", err)
		}
	default:
		t.Fatal("expected shutdown cause in shutdownErr channel")
	}

	if got := atomic.LoadInt32(&listenerCloseCount); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sessionCloseCount); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}
}

func TestHandleTCPConnectionOpenStreamFailureTriggersShutdown(t *testing.T) {
	openErr := errors.New("open stream failure")
	var listenerCloseCount int32
	var sessionCloseCount int32

	ctx := &tunnelContext{
		listener: &mockListener{
			closeFn: func() error {
				atomic.AddInt32(&listenerCloseCount, 1)
				return nil
			},
		},
		session: &mockSession{
			openStreamFn: func() (*smux.Stream, error) {
				return nil, openErr
			},
			closeFn: func() error {
				atomic.AddInt32(&sessionCloseCount, 1)
				return nil
			},
		},
		remotePort:  "8080",
		done:        make(chan struct{}),
		shutdownErr: make(chan error, 1),
	}

	clientConn, serverConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()

	handleTCPConnection(clientConn, ctx)

	select {
	case <-ctx.done:
	default:
		t.Fatal("expected done channel to be closed")
	}

	select {
	case err := <-ctx.shutdownErr:
		if !errors.Is(err, openErr) {
			t.Fatalf("shutdownErr = %v, want wrapped openErr", err)
		}
	default:
		t.Fatal("expected shutdown cause in shutdownErr channel")
	}

	if got := atomic.LoadInt32(&listenerCloseCount); got != 1 {
		t.Fatalf("listener Close() called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&sessionCloseCount); got != 1 {
		t.Fatalf("session Close() called %d times, want 1", got)
	}
}
