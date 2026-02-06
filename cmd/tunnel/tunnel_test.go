package tunnel

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/xtaci/smux"
)

type mockSession struct {
	openStreamFn func() (*smux.Stream, error)
	closeFn      func() error
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
	defer serverConn.Close()

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
