package kube

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxConnectHeaderBytes caps what a client may send before its CONNECT
	// request ends; a larger header is refused with 431.
	maxConnectHeaderBytes = 64 << 10
	// defaultConnectHeaderTimeout bounds how long a client may take to send
	// its CONNECT request.
	defaultConnectHeaderTimeout = 10 * time.Second
	// upstreamDialTimeout bounds reaching the target, including the CONNECT
	// exchange with an upstream proxy when one is configured.
	upstreamDialTimeout = 30 * time.Second
	// refusalLinger is how long a refused connection keeps draining what the
	// client still sends, so the close does not reset the refusal away.
	refusalLinger = 500 * time.Millisecond

	connectEstablished = "HTTP/1.1 200 Connection established\r\n\r\n"
)

// connectRelay is a CONNECT-only proxy that tunnels to exactly one target.
// It never reads the tunneled bytes: once the CONNECT request is accepted,
// the two connections are joined and every byte passes through unmodified,
// so TLS runs end to end between the client and the target. The upstream
// connection follows the same proxy environment as the rest of the CLI.
type connectRelay struct {
	// target is the only authority a client may CONNECT to, as host:port.
	target string
	// proxy picks the upstream proxy for the target; nil means a direct dial.
	proxy func(*http.Request) (*url.URL, error)
	// dial opens every outbound TCP connection, to the target or to a proxy.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
	// proxyRootCAs verifies an https upstream proxy; nil means the system roots.
	proxyRootCAs *x509.CertPool
	// headerTimeout bounds reading a client's CONNECT request.
	headerTimeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	closed    bool
	listeners map[net.Listener]struct{}
	conns     map[net.Conn]struct{}
	handlers  sync.WaitGroup
}

// newConnectRelay returns a relay to target, a host:port. The target is
// normalized the way each client's authority is, so the two compare as equal
// whenever they name the same host and port; a target that does not parse
// is kept as written and so matches no client, which refuses every CONNECT.
func newConnectRelay(target string) *connectRelay {
	if normalized, err := normalizeAuthority(target); err == nil {
		target = normalized
	}
	dialer := &net.Dialer{Timeout: upstreamDialTimeout, KeepAlive: 30 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	return &connectRelay{
		target:        target,
		proxy:         http.ProxyFromEnvironment,
		dial:          dialer.DialContext,
		headerTimeout: defaultConnectHeaderTimeout,
		ctx:           ctx,
		cancel:        cancel,
		listeners:     make(map[net.Listener]struct{}),
		conns:         make(map[net.Conn]struct{}),
	}
}

// normalizeAuthority turns a CONNECT authority into the form the relay
// compares: the host lowercased, an IP address in its canonical spelling, an
// IPv6 address bracketed, and the port spelled out, 443 when absent. A
// trailing dot is kept as written, so "example.com." does not match
// "example.com"; kubectl never sends one.
func normalizeAuthority(authority string) (string, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		// A bare host has no port, and neither does a bracketed or a bare
		// IPv6 address; anything else SplitHostPort refused is malformed.
		bare := strings.TrimSuffix(strings.TrimPrefix(authority, "["), "]")
		_, addrErr := netip.ParseAddr(bare)
		bracketed := strings.HasPrefix(authority, "[") && strings.HasSuffix(authority, "]")
		if strings.Contains(authority, ":") && !bracketed && addrErr != nil {
			return "", err
		}
		if bracketed && addrErr != nil {
			return "", fmt.Errorf("%q is not an IP address", bare)
		}
		host, port = bare, "443"
	}
	if host == "" {
		return "", errors.New("missing host")
	}
	if strings.ContainsAny(host, "/@ ") {
		return "", fmt.Errorf("invalid host %q", host)
	}
	n, err := strconv.Atoi(port)
	if err != nil || strings.Trim(port, "0123456789") != "" || n < 1 || n > 65535 {
		return "", fmt.Errorf("invalid port %q", port)
	}
	if addr, addrErr := netip.ParseAddr(host); addrErr == nil {
		host = addr.String()
	} else if strings.HasPrefix(authority, "[") {
		return "", fmt.Errorf("%q is not an IP address", host)
	} else {
		host = strings.ToLower(host)
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}

// Serve accepts connections on ln until Close is called, and then returns
// nil. A listener closed by anything else ends Serve with net.ErrClosed;
// every other accept failure is retried after a pause.
func (r *connectRelay) Serve(ln net.Listener) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	r.listeners[ln] = struct{}{}
	r.mu.Unlock()

	var backoff time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if r.isClosed() {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Running out of file descriptors and similar failures pass, so
			// wait and try again instead of ending the relay over them.
			backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
			select {
			case <-time.After(backoff):
			case <-r.ctx.Done():
				return nil
			}
			continue
		}
		backoff = 0
		if !r.startHandler(conn) {
			_ = conn.Close()
			return nil
		}
	}
}

// Close stops every Serve, ends every live connection, and returns once
// their handlers have finished. It is safe to call more than once.
func (r *connectRelay) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.handlers.Wait()
		return nil
	}
	r.closed = true
	r.cancel()
	var errs []error
	for ln := range r.listeners {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	for conn := range r.conns {
		_ = conn.Close()
	}
	r.mu.Unlock()
	r.handlers.Wait()
	return errors.Join(errs...)
}

func (r *connectRelay) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// startHandler registers conn and runs its handler, unless the relay is
// already closed. The handler is counted under the same lock Close takes, so
// Close never waits on a group that is still growing.
func (r *connectRelay) startHandler(conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.conns[conn] = struct{}{}
	r.handlers.Add(1)
	go func() {
		defer r.handlers.Done()
		defer r.forget(conn)
		r.handle(conn)
	}()
	return true
}

// track registers an upstream connection so Close can end it. It returns
// false once the relay is closed, and the caller then closes conn itself.
func (r *connectRelay) track(conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.conns[conn] = struct{}{}
	return true
}

// forget closes conn and stops tracking it.
func (r *connectRelay) forget(conn net.Conn) {
	_ = conn.Close()
	r.mu.Lock()
	delete(r.conns, conn)
	r.mu.Unlock()
}

func (r *connectRelay) handle(client net.Conn) {
	_ = client.SetDeadline(time.Now().Add(r.headerTimeout))
	limited := &io.LimitedReader{R: client, N: maxConnectHeaderBytes}
	br := bufio.NewReader(limited)
	req, err := http.ReadRequest(br)
	if err != nil {
		var netErr net.Error
		switch {
		case limited.N == 0:
			refuse(client, http.StatusRequestHeaderFieldsTooLarge, "request header too large", "")
		case errors.As(err, &netErr) && netErr.Timeout(), errors.Is(err, io.EOF),
			errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed):
			// The client went quiet, left before or partway through its
			// header, or the relay is closing; nobody is left to answer.
		default:
			refuse(client, http.StatusBadRequest, "malformed request", "")
		}
		return
	}
	// The header deadline bounds reading the request and nothing after it:
	// the dial below has its own bound, and a slow upstream must still get its
	// 200 written. Every refusal sets a deadline of its own.
	_ = client.SetDeadline(time.Time{})
	if req.Method != http.MethodConnect {
		refuse(client, http.StatusMethodNotAllowed, "only CONNECT is relayed", "Allow: CONNECT\r\n")
		return
	}
	authority := req.URL.Host
	if authority == "" {
		authority = req.Host
	}
	if requested, err := normalizeAuthority(authority); err != nil || requested != r.target {
		refuse(client, http.StatusForbidden, "CONNECT is only relayed to "+r.target, "")
		return
	}

	ctx, cancel := context.WithTimeout(r.ctx, upstreamDialTimeout)
	upstream, err := r.dialUpstream(ctx)
	cancel()
	if err != nil {
		refuse(client, http.StatusBadGateway, err.Error(), "")
		return
	}
	if !r.track(upstream) {
		_ = upstream.Close()
		return
	}
	defer r.forget(upstream)

	if _, err := io.WriteString(client, connectEstablished); err != nil {
		return
	}
	// The client may send its first bytes, the TLS ClientHello, in the same
	// segment as the CONNECT request; those sit in br past the header.
	if n := br.Buffered(); n > 0 {
		early, _ := br.Peek(n)
		if _, err := upstream.Write(early); err != nil {
			return
		}
	}
	_ = client.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})
	join(client, upstream)
}

// join copies both ways until both directions end. A direction that ends
// cleanly half-closes its destination so the other side sees the end of the
// stream and can still answer; a failed copy closes both connections.
func join(a, b net.Conn) {
	done := make(chan error, 2)
	pass := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		if err == nil {
			err = closeWrite(dst)
		}
		done <- err
	}
	go pass(a, b)
	go pass(b, a)
	if err := <-done; err != nil {
		_ = a.Close()
		_ = b.Close()
	}
	<-done
	_ = a.Close()
	_ = b.Close()
}

type closeWriter interface {
	CloseWrite() error
}

// closeWrite half-closes conn, or closes it outright when it cannot.
func closeWrite(conn net.Conn) error {
	if cw, ok := conn.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return conn.Close()
}

// refuse answers a request the relay will not tunnel and closes the
// connection. It half-closes first and drains for a moment, since closing
// with unread input would reset the connection and could drop the answer.
func refuse(conn net.Conn, status int, message, extraHeaders string) {
	body := message + "\n"
	_ = conn.SetDeadline(time.Now().Add(refusalLinger))
	_, err := fmt.Fprintf(conn,
		"HTTP/1.1 %d %s\r\n%sContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), extraHeaders, len(body), body)
	if err != nil {
		return
	}
	if cw, ok := conn.(closeWriter); ok && cw.CloseWrite() == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(conn, maxConnectHeaderBytes))
	}
}

// dialUpstream connects to the target, through the upstream proxy the proxy
// function picks for an https request to it, or directly when it picks none.
func (r *connectRelay) dialUpstream(ctx context.Context) (net.Conn, error) {
	var proxyURL *url.URL
	if r.proxy != nil {
		var err error
		proxyURL, err = r.proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: r.target}})
		if err != nil {
			// The error quotes the proxy setting as written, credentials
			// included, so it is not passed on.
			return nil, fmt.Errorf("failed to choose a proxy for %s: the proxy setting (HTTPS_PROXY or https_proxy) is not a valid URL", r.target)
		}
	}
	if proxyURL == nil {
		conn, err := r.dial(ctx, "tcp", r.target)
		if err != nil {
			return nil, fmt.Errorf("failed to reach %s: %w", r.target, err)
		}
		return conn, nil
	}
	switch proxyURL.Scheme {
	case "http", "https":
		return r.dialThroughProxy(ctx, proxyURL)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q for %s: only http and https proxies can be used", proxyURL.Scheme, r.target)
	}
}

// dialThroughProxy opens a CONNECT tunnel to the target through proxyURL. The
// errors name the proxy by host alone, never with its credentials.
func (r *connectRelay) dialThroughProxy(ctx context.Context, proxyURL *url.URL) (net.Conn, error) {
	host := proxyURL.Hostname()
	if host == "" {
		return nil, fmt.Errorf("proxy URL for %s has no host", r.target)
	}
	port := proxyURL.Port()
	if port == "" {
		port = "80"
		if proxyURL.Scheme == "https" {
			port = "443"
		}
	}
	address := net.JoinHostPort(host, port)

	raw, err := r.dial(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to reach proxy %s: %w", address, err)
	}
	conn := raw
	// Close the connection if ctx ends mid-handshake, which also unblocks
	// the reads and writes below.
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	fail := func(err error) (net.Conn, error) {
		stop()
		_ = raw.Close()
		return nil, err
	}

	if proxyURL.Scheme == "https" {
		tlsConn := tls.Client(raw, &tls.Config{
			ServerName: host,
			RootCAs:    r.proxyRootCAs,
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fail(fmt.Errorf("TLS handshake with proxy %s failed: %w", address, err))
		}
		conn = tlsConn
	}

	var request strings.Builder
	fmt.Fprintf(&request, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", r.target, r.target)
	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + password))
		fmt.Fprintf(&request, "Proxy-Authorization: Basic %s\r\n", credentials)
	}
	request.WriteString("\r\n")
	if _, err := io.WriteString(conn, request.String()); err != nil {
		return fail(fmt.Errorf("failed to send CONNECT to proxy %s: %w", address, err))
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return fail(fmt.Errorf("failed to read the CONNECT reply from proxy %s: %w", address, err))
	}
	// A refusal's body is never read: closing it could drain an unbounded
	// stream, and the connection is dropped anyway.
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("proxy %s refused CONNECT to %s: %s", address, r.target, resp.Status))
	}
	// A 200 reply to CONNECT has no body.
	_ = resp.Body.Close()
	if !stop() {
		// ctx ended and closed the connection after the reply arrived.
		return nil, fmt.Errorf("proxy %s: %w", address, ctx.Err())
	}
	return &proxiedConn{Conn: conn, r: br}, nil
}

// proxiedConn is a tunnel through an upstream proxy. The proxy may send the
// target's first bytes in the same segment as its reply, so reads drain the
// reader that parsed the reply before the connection itself.
type proxiedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *proxiedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// CloseWrite forwards the half-close to the connection underneath, which for
// an https proxy sends a TLS close_notify.
func (c *proxiedConn) CloseWrite() error {
	return closeWrite(c.Conn)
}
