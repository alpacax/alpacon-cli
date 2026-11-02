package testutil

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
)

// PipeServerURL is the base URL of every PipeServer; nothing resolves it, every dial is a pipe.
const PipeServerURL = "http://pipe.test"

// PipeServer serves a handler over net.Pipe connections, HTTP and WebSocket alike. A goroutine
// blocked on a pipe is durably blocked, so unlike httptest.Server it can live in a synctest
// bubble and the bubble's clock advances. Create it inside the bubble.
// TODO: replace with httptest.NewTestServer once go.mod moves to Go 1.27.
type PipeServer struct {
	URL      string
	listener *pipeListener
	server   *http.Server
	client   *http.Client
}

// NewPipeServer starts serving h and shuts down at the end of t, closing every connection.
func NewPipeServer(t *testing.T, h http.Handler) *PipeServer {
	t.Helper()
	listener := &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
	s := &PipeServer{URL: PipeServerURL, listener: listener, server: &http.Server{Handler: h}}
	s.client = &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: s.DialContext}}
	go func() { _ = s.server.Serve(listener) }()
	t.Cleanup(s.Close)
	return s
}

// SetHandler replaces the handler, for one that needs the server's URL; call it before any request.
func (s *PipeServer) SetHandler(h http.Handler) { s.server.Handler = h }

// Client returns an http.Client whose every request reaches the server.
func (s *PipeServer) Client() *http.Client { return s.client }

// Transport is Client's transport, for code that dials the way a client's transport does.
func (s *PipeServer) Transport() *http.Transport { return s.client.Transport.(*http.Transport) }

// WebsocketURL is the ws:// form of URL with path appended.
func (s *PipeServer) WebsocketURL(path string) string { return "ws" + s.URL[len("http"):] + path }

// DialContext opens a new pipe to the server whatever the address.
func (s *PipeServer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case s.listener.conns <- server:
		s.listener.track(server)
		return client, nil
	case <-s.listener.done:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close stops the server and drops every connection, a hijacked WebSocket's included;
// it is safe to call twice.
func (s *PipeServer) Close() {
	_ = s.server.Close()
	s.listener.closeAccepted()
	s.client.CloseIdleConnections()
}

type pipeListener struct {
	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	accepted  []net.Conn // http.Server.Close skips hijacked connections, so Close ends these itself
}

func (l *pipeListener) track(conn net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.accepted = append(l.accepted, conn)
}

func (l *pipeListener) closeAccepted() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, conn := range l.accepted {
		_ = conn.Close()
	}
	l.accepted = nil
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe.test" }
