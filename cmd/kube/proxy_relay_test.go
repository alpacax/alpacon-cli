package kube

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relayTestTimeout bounds every blocking step, so a relay bug fails a test
// instead of hanging it.
const relayTestTimeout = 10 * time.Second

// directProxy is the proxy function for a relay that dials the target
// itself, whatever the environment of the test process says.
func directProxy(*http.Request) (*url.URL, error) { return nil, nil }

// startRelay runs a relay to target on a loopback port with its proxy
// function and dial counter in place, and closes it at cleanup, checking that
// Serve then returned nil. configure, when set, adjusts the relay before it
// starts serving; the counter wraps whatever dial function it leaves.
func startRelay(t *testing.T, target string, configure func(*connectRelay)) (*connectRelay, string, *atomic.Int32) {
	t.Helper()
	relay := newConnectRelay(target)
	relay.proxy = directProxy
	if configure != nil {
		configure(relay)
	}
	dials := &atomic.Int32{}
	dial := relay.dial
	relay.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return dial(ctx, network, address)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	served := make(chan error, 1)
	go func() { served <- relay.Serve(ln) }()
	t.Cleanup(func() {
		assert.NoError(t, relay.Close())
		select {
		case err := <-served:
			assert.NoError(t, err)
		case <-time.After(relayTestTimeout):
			t.Error("Serve did not return after Close")
		}
	})
	return relay, ln.Addr().String(), dials
}

// listenTCP starts a loopback listener that hands each accepted connection
// to handle on its own goroutine. Cleanup closes it and waits for every
// handler, so none runs on into the next test; register it before the relay,
// whose cleanup then ends the connections first.
func listenTCP(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var handlers sync.WaitGroup
	t.Cleanup(func() {
		_ = ln.Close()
		waitForHandlers(t, &handlers)
	})
	handlers.Add(1)
	go func() {
		defer handlers.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(relayTestTimeout))
				handle(conn)
			}()
		}
	}()
	return ln.Addr().String()
}

// waitForHandlers fails the test if a server goroutine it started is still
// running after relayTestTimeout.
func waitForHandlers(t *testing.T, handlers *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		handlers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(relayTestTimeout):
		t.Error("a test server handler did not return")
	}
}

// dialRelay opens a client connection to the relay under the test deadline.
func dialRelay(t *testing.T, relayAddr string) *net.TCPConn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", relayAddr, relayTestTimeout)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(relayTestTimeout)))
	return conn.(*net.TCPConn)
}

// sendRaw writes request to the relay and reads back its answer.
func sendRaw(t *testing.T, relayAddr, request string) (*http.Response, *bufio.Reader) {
	t.Helper()
	conn := dialRelay(t, relayAddr)
	_, err := io.WriteString(conn, request)
	require.NoError(t, err)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp, br
}

// connectThrough opens a tunnel to target and checks the exact
// established line, returning the connection and the reader past it.
func connectThrough(t *testing.T, relayAddr, target string) (*net.TCPConn, *bufio.Reader) {
	t.Helper()
	conn := dialRelay(t, relayAddr)
	_, err := io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	require.NoError(t, err)
	br := bufio.NewReader(conn)
	line := make([]byte, len(connectEstablished))
	_, err = io.ReadFull(br, line)
	require.NoError(t, err)
	require.Equal(t, connectEstablished, string(line))
	return conn, br
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

func TestNewConnectRelayDefaults(t *testing.T) {
	t.Parallel()
	relay := newConnectRelay("Proxy.Example.com")

	require.NotNil(t, relay.proxy)
	assert.Equal(t, reflect.ValueOf(http.ProxyFromEnvironment).Pointer(), reflect.ValueOf(relay.proxy).Pointer())
	assert.Equal(t, "proxy.example.com:443", relay.target)
	assert.Equal(t, defaultConnectHeaderTimeout, relay.headerTimeout)
	assert.NotNil(t, relay.dial)
	assert.Nil(t, relay.proxyRootCAs)
	assert.NoError(t, relay.Close())
}

func TestNormalizeAuthority(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		authority string
		want      string
		wantErr   bool
	}{
		{name: "host and port", authority: "proxy.example.com:6443", want: "proxy.example.com:6443"},
		{name: "default port", authority: "proxy.example.com", want: "proxy.example.com:443"},
		{name: "mixed case host", authority: "Proxy.EXAMPLE.com:443", want: "proxy.example.com:443"},
		{name: "ipv4", authority: "127.0.0.1:8443", want: "127.0.0.1:8443"},
		{name: "bracketed ipv6 with port", authority: "[::1]:443", want: "[::1]:443"},
		{name: "bracketed ipv6 without port", authority: "[::1]", want: "[::1]:443"},
		{name: "bare ipv6", authority: "::1", want: "[::1]:443"},
		{name: "ipv6 long form", authority: "[0:0:0:0:0:0:0:1]:443", want: "[::1]:443"},
		{name: "ipv6 upper case", authority: "[2001:DB8::1]:443", want: "[2001:db8::1]:443"},
		{name: "leading zero port", authority: "proxy.example.com:0443", want: "proxy.example.com:443"},
		{name: "trailing dot kept", authority: "proxy.example.com.:443", want: "proxy.example.com.:443"},
		{name: "empty", authority: "", wantErr: true},
		{name: "empty host", authority: ":443", wantErr: true},
		{name: "port zero", authority: "proxy.example.com:0", wantErr: true},
		{name: "port too large", authority: "proxy.example.com:65536", wantErr: true},
		{name: "named port", authority: "proxy.example.com:https", wantErr: true},
		{name: "signed port", authority: "proxy.example.com:+443", wantErr: true},
		{name: "empty port", authority: "proxy.example.com:", wantErr: true},
		{name: "bracketed name", authority: "[proxy.example.com]:443", wantErr: true},
		{name: "bracketed name without port", authority: "[proxy.example.com]", wantErr: true},
		{name: "userinfo", authority: "alice@proxy.example.com:443", wantErr: true},
		{name: "path", authority: "proxy.example.com/x:443", wantErr: true},
		{name: "two colons", authority: "proxy.example.com:443:1", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeAuthority(tc.authority)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestConnectRelayCarriesPayloadBothWays(t *testing.T) {
	t.Parallel()
	const size = 1 << 20
	request := randomBytes(t, size)
	response := randomBytes(t, size)

	upstreamGot := make(chan []byte, 1)
	// The upstream reads to the end of the stream before it answers, so the
	// answer arrives only if the client's half-close crossed the relay and
	// the relay kept the other direction open.
	target := listenTCP(t, func(conn net.Conn) {
		got, _ := io.ReadAll(conn)
		upstreamGot <- got
		_, _ = conn.Write(response)
	})
	_, relayAddr, _ := startRelay(t, target, nil)

	conn, br := connectThrough(t, relayAddr, target)
	_, err := conn.Write(request)
	require.NoError(t, err)
	require.NoError(t, conn.CloseWrite())

	got, err := io.ReadAll(br)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(response, got), "response arrived with %d of %d bytes or altered", len(got), size)

	select {
	case received := <-upstreamGot:
		assert.True(t, bytes.Equal(request, received), "request arrived with %d of %d bytes or altered", len(received), size)
	case <-time.After(relayTestTimeout):
		t.Fatal("upstream never saw the end of the request")
	}
}

func TestConnectRelayHTTPClientKeepsHost(t *testing.T) {
	t.Parallel()
	hosts := make(chan string, 1)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts <- r.Host
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(ts.Close)
	target := ts.Listener.Addr().String()
	_, relayAddr, dials := startRelay(t, target, nil)

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	transport := &http.Transport{
		Proxy:           http.ProxyURL(&url.URL{Scheme: "http", Host: relayAddr}),
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: relayTestTimeout}

	resp, err := client.Get(ts.URL + "/api")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, target, <-hosts)
	assert.Equal(t, int32(1), dials.Load())
}

func TestConnectRelayCarriesWebSocket(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	// An upgraded connection is hijacked, so the server's Close neither ends
	// nor waits for its handler; the test waits for it itself, or the handler
	// would run on into whichever test comes next.
	handlerDone := make(chan struct{})
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = ws.Close() }()
		for {
			kind, message, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := ws.WriteMessage(kind, message); err != nil {
				return
			}
		}
	}))
	t.Cleanup(ts.Close)
	target := ts.Listener.Addr().String()
	_, relayAddr, _ := startRelay(t, target, nil)

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	dialer := websocket.Dialer{
		Proxy:            http.ProxyURL(&url.URL{Scheme: "http", Host: relayAddr}),
		TLSClientConfig:  &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		HandshakeTimeout: relayTestTimeout,
	}
	ctx, cancel := context.WithTimeout(context.Background(), relayTestTimeout)
	defer cancel()
	ws, resp, err := dialer.DialContext(ctx, "wss://"+target+"/ws", nil)
	require.NoError(t, err)
	defer func() {
		_ = ws.Close()
		select {
		case <-handlerDone:
		case <-time.After(relayTestTimeout):
			t.Error("the WebSocket handler did not return after the client closed")
		}
	}()
	_ = resp.Body.Close()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(relayTestTimeout)))

	payload := randomBytes(t, 64<<10)
	require.NoError(t, ws.WriteMessage(websocket.BinaryMessage, payload))
	kind, echoed, err := ws.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, kind)
	assert.Equal(t, payload, echoed)

	// A second client alongside the open WebSocket gets the exact
	// established line, and its tunnel reaches the same TLS server.
	conn, br := connectThrough(t, relayAddr, target)
	tlsConn := tls.Client(&bufferedTestConn{Conn: conn, r: br}, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
	require.NoError(t, tlsConn.HandshakeContext(ctx))
	_ = tlsConn.Close()
}

// bufferedTestConn reads through r, which may already hold bytes read off
// the connection.
type bufferedTestConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedTestConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func TestConnectRelayRefusesWithoutDialing(t *testing.T) {
	t.Parallel()
	const target = "proxy.example.com:443"
	tests := []struct {
		name       string
		request    string
		wantStatus int
		wantHeader string
	}{
		{
			name:       "other host",
			request:    "CONNECT other.example.com:443 HTTP/1.1\r\nHost: other.example.com:443\r\n\r\n",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "other port",
			request:    "CONNECT proxy.example.com:6443 HTTP/1.1\r\nHost: proxy.example.com:6443\r\n\r\n",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "subdomain of target",
			request:    "CONNECT evil.proxy.example.com:443 HTTP/1.1\r\nHost: evil.proxy.example.com:443\r\n\r\n",
			wantStatus: http.StatusForbidden,
		},
		// A CONNECT authority http.ReadRequest cannot parse is refused as
		// malformed before it reaches the comparison.
		{
			name:       "malformed authority",
			request:    "CONNECT proxy.example.com:https HTTP/1.1\r\nHost: proxy.example.com\r\n\r\n",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "get",
			request:    "GET https://proxy.example.com/ HTTP/1.1\r\nHost: proxy.example.com\r\n\r\n",
			wantStatus: http.StatusMethodNotAllowed,
			wantHeader: "CONNECT",
		},
		{
			name:       "origin form get",
			request:    "GET / HTTP/1.1\r\nHost: proxy.example.com\r\n\r\n",
			wantStatus: http.StatusMethodNotAllowed,
			wantHeader: "CONNECT",
		},
		{
			name:       "malformed request line",
			request:    "NOT HTTP\r\n\r\n",
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, relayAddr, dials := startRelay(t, target, nil)

			resp, _ := sendRaw(t, relayAddr, tc.request)
			assert.Equal(t, tc.wantStatus, resp.StatusCode)
			assert.Equal(t, tc.wantHeader, resp.Header.Get("Allow"))
			assert.Zero(t, dials.Load())
		})
	}
}

func TestConnectRelayAcceptsEquivalentAuthority(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		target    string
		requested string
		wantDial  string
	}{
		{name: "host case", target: "Cluster.Example.com:6443", requested: "cluster.example.COM:6443", wantDial: "cluster.example.com:6443"},
		{name: "default port", target: "cluster.example.com", requested: "cluster.example.com:443", wantDial: "cluster.example.com:443"},
		{name: "explicit port against default", target: "cluster.example.com:443", requested: "cluster.example.com", wantDial: "cluster.example.com:443"},
		{name: "ipv6 spelling", target: "[::1]:443", requested: "[0:0:0:0:0:0:0:1]:443", wantDial: "[::1]:443"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			echo := listenTCP(t, func(conn net.Conn) { _, _ = io.Copy(conn, conn) })
			dialed := make(chan string, 1)
			// The dial goes to a local echo whatever the target names, so the
			// test needs no name resolution.
			_, relayAddr, _ := startRelay(t, tc.target, func(r *connectRelay) {
				r.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
					dialed <- address
					var d net.Dialer
					return d.DialContext(ctx, network, echo)
				}
			})

			conn, br := connectThrough(t, relayAddr, tc.requested)
			assert.Equal(t, tc.wantDial, <-dialed)
			_, err := io.WriteString(conn, "ping")
			require.NoError(t, err)
			got := make([]byte, 4)
			_, err = io.ReadFull(br, got)
			require.NoError(t, err)
			assert.Equal(t, "ping", string(got))
		})
	}
}

func TestConnectRelayRefusesOversizedHeader(t *testing.T) {
	t.Parallel()
	_, relayAddr, dials := startRelay(t, "proxy.example.com:443", nil)
	conn := dialRelay(t, relayAddr)

	var request strings.Builder
	request.WriteString("CONNECT proxy.example.com:443 HTTP/1.1\r\nHost: proxy.example.com:443\r\n")
	for request.Len() <= maxConnectHeaderBytes+4096 {
		request.WriteString("X-Pad: " + strings.Repeat("a", 1000) + "\r\n")
	}
	request.WriteString("\r\n")

	// The relay may stop reading before the whole header is written, so the
	// write may fail; what matters is that no tunnel opens.
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = io.WriteString(conn, request.String())
	}()
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err == nil {
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusRequestHeaderFieldsTooLarge, resp.StatusCode)
	} else {
		var netErr net.Error
		assert.False(t, errors.As(err, &netErr) && netErr.Timeout(), "the relay neither answered nor closed: %v", err)
	}
	_ = conn.Close()
	<-writeDone
	assert.Zero(t, dials.Load())
}

func TestConnectRelayDialFailureIsBadGateway(t *testing.T) {
	t.Parallel()
	const target = "proxy.example.com:443"
	_, relayAddr, dials := startRelay(t, target, func(r *connectRelay) {
		r.dial = func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("connection refused")
		}
	})

	resp, _ := sendRaw(t, relayAddr, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, string(body), "connection refused")
	assert.Equal(t, int32(1), dials.Load())
}

func TestConnectRelayDeliversPipelinedBytes(t *testing.T) {
	t.Parallel()
	// Larger than the header reader's buffer, so part of it is still on the
	// socket when the header has been parsed.
	early := randomBytes(t, 10<<10)
	upstreamGot := make(chan []byte, 1)
	target := listenTCP(t, func(conn net.Conn) {
		got := make([]byte, len(early))
		_, err := io.ReadFull(conn, got)
		if err != nil {
			t.Errorf("upstream read: %v", err)
		}
		upstreamGot <- got
	})
	_, relayAddr, _ := startRelay(t, target, nil)

	conn := dialRelay(t, relayAddr)
	header := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n\r\n"
	_, err := conn.Write(append([]byte(header), early...))
	require.NoError(t, err)

	line := make([]byte, len(connectEstablished))
	_, err = io.ReadFull(conn, line)
	require.NoError(t, err)
	assert.Equal(t, connectEstablished, string(line))
	select {
	case got := <-upstreamGot:
		assert.Equal(t, early, got)
	case <-time.After(relayTestTimeout):
		t.Fatal("upstream never received the pipelined bytes")
	}
}

func TestConnectRelayCloseEndsLiveConnections(t *testing.T) {
	t.Parallel()
	upstreamClosed := make(chan struct{})
	target := listenTCP(t, func(conn net.Conn) {
		_, _ = io.Copy(io.Discard, conn)
		close(upstreamClosed)
	})
	relay := newConnectRelay(target)
	relay.proxy = directProxy
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	served := make(chan error, 1)
	go func() { served <- relay.Serve(ln) }()
	relayAddr := ln.Addr().String()

	tunnel, br := connectThrough(t, relayAddr, target)
	// A client that connected but never sent its request must not hold
	// Close up until the header timeout.
	idle := dialRelay(t, relayAddr)

	closed := make(chan error, 1)
	go func() { closed <- relay.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(relayTestTimeout):
		t.Fatal("Close did not return")
	}
	select {
	case err := <-served:
		require.NoError(t, err)
	case <-time.After(relayTestTimeout):
		t.Fatal("Serve did not return after Close")
	}

	_, tunnelErr := br.ReadByte()
	require.Error(t, tunnelErr)
	_, idleErr := idle.Read(make([]byte, 1))
	require.Error(t, idleErr)
	select {
	case <-upstreamClosed:
	case <-time.After(relayTestTimeout):
		t.Fatal("the upstream connection stayed open")
	}
	_ = tunnel.Close()
	assert.NoError(t, relay.Close())
}

func TestConnectRelayServeAfterCloseReturnsNil(t *testing.T) {
	t.Parallel()
	relay := newConnectRelay("proxy.example.com:443")
	require.NoError(t, relay.Close())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	assert.NoError(t, relay.Serve(ln))
	_, err = ln.Accept()
	assert.ErrorIs(t, err, net.ErrClosed)
}

// fakeUpstreamProxy answers CONNECT the way a forward proxy does, with
// status, and in the same write sends greeting as the target's first
// bytes; it then echoes the tunnel. It records each request it saw.
type fakeUpstreamProxy struct {
	status   string
	greeting string

	mu       sync.Mutex
	requests []*http.Request
	handlers sync.WaitGroup
}

// startUpstreamProxy serves p, over TLS when useTLS is set. A hijacked
// connection is one the server's Close neither ends nor waits for, so cleanup
// waits for p's handlers itself; Close returns only once every handler has
// hijacked or finished, so each Add below happens before that wait.
func startUpstreamProxy(t *testing.T, p *fakeUpstreamProxy, useTLS bool) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(p)
	if useTLS {
		ts.StartTLS()
	} else {
		ts.Start()
	}
	t.Cleanup(func() {
		ts.Close()
		waitForHandlers(t, &p.handlers)
	})
	return ts
}

func (p *fakeUpstreamProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.handlers.Add(1)
	defer p.handlers.Done()
	p.mu.Lock()
	p.requests = append(p.requests, r)
	p.mu.Unlock()
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(relayTestTimeout))
	// One write carries both the reply and the target's first bytes.
	if _, err := io.WriteString(conn, "HTTP/1.1 "+p.status+"\r\nContent-Length: 0\r\n\r\n"+p.greeting); err != nil {
		return
	}
	if !strings.HasPrefix(p.status, "200") {
		return
	}
	_, _ = io.Copy(conn, rw.Reader)
}

func (p *fakeUpstreamProxy) seen() []*http.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*http.Request(nil), p.requests...)
}

func TestConnectRelayChainsThroughUpstreamProxy(t *testing.T) {
	t.Parallel()
	const target = "cluster.example.com:443"
	tests := []struct {
		name   string
		useTLS bool
	}{
		{name: "http proxy", useTLS: false},
		{name: "https proxy", useTLS: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			upstream := &fakeUpstreamProxy{status: "200 Connection established", greeting: "first upstream bytes"}
			ts := startUpstreamProxy(t, upstream, tc.useTLS)
			proxyURL, err := url.Parse(ts.URL)
			require.NoError(t, err)
			proxyURL.User = url.UserPassword("alice", "s3cr:et")

			var asked atomic.Pointer[url.URL]
			_, relayAddr, dials := startRelay(t, target, func(r *connectRelay) {
				r.proxy = func(req *http.Request) (*url.URL, error) {
					asked.Store(req.URL)
					return proxyURL, nil
				}
				if tc.useTLS {
					pool := x509.NewCertPool()
					pool.AddCert(ts.Certificate())
					r.proxyRootCAs = pool
				}
			})

			conn, br := connectThrough(t, relayAddr, target)
			greeting := make([]byte, len(upstream.greeting))
			_, err = io.ReadFull(br, greeting)
			require.NoError(t, err)
			assert.Equal(t, upstream.greeting, string(greeting))

			_, err = io.WriteString(conn, "ping")
			require.NoError(t, err)
			require.NoError(t, conn.CloseWrite())
			rest, err := io.ReadAll(br)
			require.NoError(t, err)
			assert.Equal(t, "ping", string(rest))

			require.NotNil(t, asked.Load())
			assert.Equal(t, "https://"+target, asked.Load().String())
			assert.Equal(t, int32(1), dials.Load())
			requests := upstream.seen()
			require.Len(t, requests, 1)
			assert.Equal(t, http.MethodConnect, requests[0].Method)
			assert.Equal(t, target, requests[0].Host)
			assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("alice:s3cr:et")), requests[0].Header.Get("Proxy-Authorization"))
		})
	}
}

func TestConnectRelayOmitsProxyAuthorizationWithoutUserinfo(t *testing.T) {
	t.Parallel()
	const target = "cluster.example.com:443"
	upstream := &fakeUpstreamProxy{status: "200 OK", greeting: "hi"}
	ts := startUpstreamProxy(t, upstream, false)
	proxyURL, err := url.Parse(ts.URL)
	require.NoError(t, err)
	_, relayAddr, _ := startRelay(t, target, func(r *connectRelay) {
		r.proxy = http.ProxyURL(proxyURL)
	})

	_, br := connectThrough(t, relayAddr, target)
	greeting := make([]byte, 2)
	_, err = io.ReadFull(br, greeting)
	require.NoError(t, err)

	requests := upstream.seen()
	require.Len(t, requests, 1)
	assert.Empty(t, requests[0].Header.Values("Proxy-Authorization"))
}

func TestConnectRelayUpstreamProxyRefusalIsBadGateway(t *testing.T) {
	t.Parallel()
	const target = "cluster.example.com:443"
	upstream := &fakeUpstreamProxy{status: "407 Proxy Authentication Required", greeting: "must not reach the client"}
	ts := startUpstreamProxy(t, upstream, false)
	proxyURL, err := url.Parse(ts.URL)
	require.NoError(t, err)
	proxyURL.User = url.UserPassword("alice", "top-secret")
	_, relayAddr, _ := startRelay(t, target, func(r *connectRelay) {
		r.proxy = http.ProxyURL(proxyURL)
	})

	resp, _ := sendRaw(t, relayAddr, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, string(body), "407")
	assert.NotContains(t, string(body), "top-secret")
	assert.NotContains(t, string(body), "must not reach the client")
}

func TestConnectRelayHTTPSProxyUntrustedCertificateIsBadGateway(t *testing.T) {
	t.Parallel()
	const target = "cluster.example.com:443"
	ts := startUpstreamProxy(t, &fakeUpstreamProxy{status: "200 OK"}, true)
	proxyURL, err := url.Parse(ts.URL)
	require.NoError(t, err)
	// No proxyRootCAs: the test server's certificate is not in the system
	// roots, so the handshake must fail rather than send CONNECT.
	_, relayAddr, _ := startRelay(t, target, func(r *connectRelay) {
		r.proxy = http.ProxyURL(proxyURL)
	})

	resp, _ := sendRaw(t, relayAddr, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, string(body), "TLS handshake")
}

func TestConnectRelayRefusesOtherProxySchemes(t *testing.T) {
	t.Parallel()
	const target = "cluster.example.com:443"
	tests := []struct {
		name   string
		scheme string
	}{
		{name: "socks5", scheme: "socks5"},
		{name: "socks5h", scheme: "socks5h"},
		{name: "ftp", scheme: "ftp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, relayAddr, dials := startRelay(t, target, func(r *connectRelay) {
				r.proxy = http.ProxyURL(&url.URL{Scheme: tc.scheme, Host: "127.0.0.1:1080"})
			})

			resp, _ := sendRaw(t, relayAddr, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
			assert.Contains(t, string(body), `"`+tc.scheme+`"`)
			assert.Zero(t, dials.Load())
		})
	}
}

func TestConnectRelayProxyFunctionErrorIsBadGateway(t *testing.T) {
	t.Parallel()
	const target = "cluster.example.com:443"
	_, relayAddr, dials := startRelay(t, target, func(r *connectRelay) {
		// http.ProxyFromEnvironment quotes the setting as written, so its
		// error can carry the proxy's password.
		r.proxy = func(*http.Request) (*url.URL, error) {
			return nil, errors.New(`invalid proxy address "http://alice:top-secret@proxy example:8080"`)
		}
	})

	resp, _ := sendRaw(t, relayAddr, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, string(body), "not a valid URL")
	assert.NotContains(t, string(body), "top-secret")
	assert.Zero(t, dials.Load())
}

func TestConnectRelayHeaderTimeoutClosesSilentClient(t *testing.T) {
	t.Parallel()
	_, relayAddr, dials := startRelay(t, "proxy.example.com:443", func(r *connectRelay) {
		r.headerTimeout = 100 * time.Millisecond
	})
	conn := dialRelay(t, relayAddr)

	_, err := io.WriteString(conn, "CONNECT proxy.example.com:443 HTTP/1.1\r\n")
	require.NoError(t, err)
	got, err := io.ReadAll(conn)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, dials.Load())
}

func TestConnectRelayClientLeavingMidHeaderGetsNoReply(t *testing.T) {
	t.Parallel()
	_, relayAddr, dials := startRelay(t, "proxy.example.com:443", nil)
	conn := dialRelay(t, relayAddr)

	_, err := io.WriteString(conn, "CONNECT proxy.example.com:443 HTTP/1.1\r\nHost: proxy.exa")
	require.NoError(t, err)
	require.NoError(t, conn.CloseWrite())
	got, err := io.ReadAll(conn)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, dials.Load())
}

func TestConnectRelayUnparsableTargetRefusesEveryConnect(t *testing.T) {
	t.Parallel()
	const target = "proxy.example.com:443:1"
	_, relayAddr, dials := startRelay(t, target, nil)

	resp, _ := sendRaw(t, relayAddr, "CONNECT proxy.example.com:443 HTTP/1.1\r\nHost: proxy.example.com:443\r\n\r\n")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Zero(t, dials.Load())
}

func TestConnectRelayCloseCancelsPendingDial(t *testing.T) {
	t.Parallel()
	const target = "cluster.example.com:443"
	dialing := make(chan struct{})
	relay := newConnectRelay(target)
	relay.proxy = directProxy
	// The dial hangs until its context ends, as one to an unresponsive host
	// does until the connect timeout.
	relay.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialing)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	served := make(chan error, 1)
	go func() { served <- relay.Serve(ln) }()

	conn := dialRelay(t, ln.Addr().String())
	_, err = io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	require.NoError(t, err)
	select {
	case <-dialing:
	case <-time.After(relayTestTimeout):
		t.Fatal("the relay never dialed")
	}

	closed := make(chan error, 1)
	go func() { closed <- relay.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(relayTestTimeout):
		t.Fatal("Close waited on the pending dial")
	}
	select {
	case err := <-served:
		require.NoError(t, err)
	case <-time.After(relayTestTimeout):
		t.Fatal("Serve did not return after Close")
	}
	// No tunnel opened: the client sees no established line, only the end
	// of the connection, possibly after a refusal.
	got, _ := io.ReadAll(conn)
	assert.NotContains(t, string(got), connectEstablished)
}

func TestConnectRelayServeReportsListenerClosedElsewhere(t *testing.T) {
	t.Parallel()
	relay := newConnectRelay("proxy.example.com:443")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	served := make(chan error, 1)
	go func() { served <- relay.Serve(ln) }()

	// Serve registers ln before its first Accept; closing it at any point
	// must end Serve with net.ErrClosed rather than a retry loop.
	require.NoError(t, ln.Close())
	select {
	case err := <-served:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(relayTestTimeout):
		t.Fatal("Serve kept retrying a closed listener")
	}
	assert.NoError(t, relay.Close())
}

// The header deadline bounds reading the CONNECT request only: an upstream
// that takes longer than it to reach, through a slow proxy say, still gets
// the tunnel established instead of a dropped connection.
func TestConnectRelaySlowDialOutlastsTheHeaderDeadline(t *testing.T) {
	t.Parallel()
	target := listenTCP(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})
	_, relayAddr, _ := startRelay(t, target, func(r *connectRelay) {
		r.headerTimeout = 100 * time.Millisecond
		dial := r.dial
		r.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			select {
			case <-time.After(400 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return dial(ctx, network, address)
		}
	})

	conn, br := connectThrough(t, relayAddr, target)
	_, err := io.WriteString(conn, "ping")
	require.NoError(t, err)
	echo := make([]byte, 4)
	_, err = io.ReadFull(br, echo)
	require.NoError(t, err)
	assert.Equal(t, "ping", string(echo))
}
