package runtime

import (
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xtaci/smux"
)

type halfCloser interface {
	net.Conn
	CloseWrite() error
}

// tcpPair returns both ends of a loopback TCP connection, which supports CloseWrite.
func tcpPair(t *testing.T) (dialed, accepted *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		conn, err := ln.Accept()
		acceptCh <- acceptResult{conn, err}
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	res := <-acceptCh
	require.NoError(t, res.err)
	t.Cleanup(func() { _ = res.conn.Close() })
	return client.(*net.TCPConn), res.conn.(*net.TCPConn)
}

// smuxPair runs a session over cliConn and agentConn with the tunnel's smux config.
func smuxPair(t *testing.T, cliConn, agentConn net.Conn) (cliSession, agentSession *smux.Session) {
	t.Helper()
	cliSession, err := smux.Client(cliConn, config.GetSmuxConfig())
	require.NoError(t, err)
	agentSession, err = smux.Server(agentConn, config.GetSmuxConfig())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = cliSession.Close()
		_ = agentSession.Close()
	})
	return cliSession, agentSession
}

// acceptRemote accepts the stream the CLI opened and consumes its metadata line one byte at a time,
// so no payload is read past it.
func acceptRemote(t *testing.T, agentSession *smux.Session) *smux.Stream {
	t.Helper()
	remote, err := agentSession.AcceptStream()
	require.NoError(t, err)
	t.Cleanup(func() { _ = remote.Close() })
	require.NoError(t, remote.SetReadDeadline(time.Now().Add(5*time.Second)))
	b := make([]byte, 1)
	for b[0] != '\n' {
		_, err := io.ReadFull(remote, b)
		require.NoError(t, err)
	}
	require.NoError(t, remote.SetReadDeadline(time.Time{}))
	return remote
}

type relayRun struct {
	done chan struct{}
	cut  bool
}

func startRelay(local, remote net.Conn, bound time.Duration) *relayRun {
	run := &relayRun{done: make(chan struct{})}
	go func() {
		run.cut = relay(local, remote, bound)
		close(run.done)
	}()
	return run
}

func (r *relayRun) finished() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// wait returns whether the bound cut the relay, failing instead of hanging when it never returns.
func (r *relayRun) wait(t *testing.T) bool {
	t.Helper()
	select {
	case <-r.done:
		return r.cut
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not return")
		return false
	}
}

// startTunnelConnection runs handleTCPConnection over a real smux session carried by a net.Pipe.
func startTunnelConnection(t *testing.T) (local *net.TCPConn, remote *smux.Stream, cliConn net.Conn, done <-chan struct{}) {
	t.Helper()
	cliConn, agentConn := net.Pipe()
	cliSession, agentSession := smuxPair(t, cliConn, agentConn)
	local, accepted := tcpPair(t)
	r := &Runtime{session: cliSession, remotePort: "80"}
	finished := make(chan struct{})
	go func() {
		r.handleTCPConnection(accepted)
		close(finished)
	}()
	return local, acceptRemote(t, agentSession), cliConn, finished
}

// startStreamRelay relays a local TCP connection to a smux stream carried over cliConn and agentConn.
func startStreamRelay(t *testing.T, cliConn, agentConn net.Conn, bound time.Duration) (local *net.TCPConn, remote *smux.Stream, run *relayRun) {
	t.Helper()
	cliSession, agentSession := smuxPair(t, cliConn, agentConn)
	stream, err := cliSession.OpenStream()
	require.NoError(t, err)
	_, err = stream.Write([]byte{'\n'})
	require.NoError(t, err)
	remote = acceptRemote(t, agentSession)
	local, accepted := tcpPair(t)
	return local, remote, startRelay(accepted, stream, bound)
}

func startPipeStreamRelay(t *testing.T, bound time.Duration) (*net.TCPConn, *smux.Stream, *relayRun) {
	t.Helper()
	cliConn, agentConn := net.Pipe()
	return startStreamRelay(t, cliConn, agentConn, bound)
}

// startPipeRelay relays two net.Pipe pairs, whose clocks follow a synctest bubble.
func startPipeRelay(bound time.Duration) (local, remote net.Conn, run *relayRun) {
	local, cliSide := net.Pipe()
	cliStream, remote := net.Pipe()
	return local, remote, startRelay(cliSide, cliStream, bound)
}

// readAllWithin reads conn to EOF, failing instead of hanging when the relay never ends it.
func readAllWithin(t *testing.T, conn net.Conn) string {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	data, err := io.ReadAll(conn)
	require.NoError(t, err)
	return string(data)
}

func sendAndHalfClose(t *testing.T, from halfCloser, to net.Conn, msg string) {
	t.Helper()
	_, err := from.Write([]byte(msg))
	require.NoError(t, err)
	require.NoError(t, from.CloseWrite())
	assert.Equal(t, msg, readAllWithin(t, to))
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connection handler did not return")
	}
}

// writeUntilError writes to conn until a write fails, signaling the first success on started.
func writeUntilError(conn net.Conn) (started <-chan struct{}, failed <-chan error) {
	startedCh := make(chan struct{})
	failedCh := make(chan error, 1)
	go func() {
		chunk := make([]byte, 4096)
		var once sync.Once
		for {
			if _, err := conn.Write(chunk); err != nil {
				failedCh <- err
				return
			}
			once.Do(func() { close(startedCh) })
		}
	}()
	return startedCh, failedCh
}

// gatedConn holds every Write once blocked, like a transport whose send buffer is full.
type gatedConn struct {
	net.Conn
	mu          sync.Mutex
	blocked     bool
	release     chan struct{}
	releaseOnce sync.Once
	stalled     chan struct{}
	stallOnce   sync.Once
}

func newGatedConn(conn net.Conn) *gatedConn {
	return &gatedConn{Conn: conn, release: make(chan struct{}), stalled: make(chan struct{})}
}

func (g *gatedConn) block() {
	g.mu.Lock()
	g.blocked = true
	g.mu.Unlock()
}

func (g *gatedConn) unblock() {
	g.releaseOnce.Do(func() { close(g.release) })
}

func (g *gatedConn) Write(b []byte) (int, error) {
	g.mu.Lock()
	blocked := g.blocked
	g.mu.Unlock()
	if blocked {
		g.stallOnce.Do(func() { close(g.stalled) })
		<-g.release
	}
	return g.Conn.Write(b)
}

func TestHandleTCPConnectionDeliversResponseAfterLocalClientHalfCloses(t *testing.T) {
	t.Parallel()
	local, remote, _, done := startTunnelConnection(t)

	sendAndHalfClose(t, local, remote, "request")
	sendAndHalfClose(t, remote, local, "response")

	waitDone(t, done)
}

func TestHandleTCPConnectionCarriesLocalDataAfterRemoteHalfCloses(t *testing.T) {
	t.Parallel()
	local, remote, _, done := startTunnelConnection(t)

	sendAndHalfClose(t, remote, local, "banner")
	sendAndHalfClose(t, local, remote, "late request")

	waitDone(t, done)
}

// The CLI end is closed, as a runtime shutdown closes its WebSocket: a net.Pipe whose agent end closed
// reads io.EOF, which a WebSocket never returns, and smux would pass it on as a clean end.
func TestHandleTCPConnectionClosesLocalClientWhenTunnelTransportCloses(t *testing.T) {
	t.Parallel()
	local, _, cliConn, done := startTunnelConnection(t)

	require.NoError(t, cliConn.Close())

	waitDone(t, done)
	assert.Empty(t, readAllWithin(t, local))
}

func TestRelayEndsBothDirectionsOnCopyErrorWithoutCut(t *testing.T) {
	t.Parallel()
	local, remote, run := startPipeStreamRelay(t, time.Hour)

	require.NoError(t, local.SetLinger(0))
	require.NoError(t, local.Close())

	assert.False(t, run.wait(t), "a copy error is not a cut")
	assert.Empty(t, readAllWithin(t, remote))
}

// Serial and on the real clock: smux pools its channels package-wide, so it cannot run in a synctest
// bubble, and the writer must keep writing for the whole bound.
func TestRelayCutsBusyLocalClientAtBoundAfterRemoteCloses(t *testing.T) {
	const bound = 200 * time.Millisecond
	local, remote, run := startPipeStreamRelay(t, bound)
	started, writeErr := writeUntilError(local)
	<-started

	closedAt := time.Now()
	require.NoError(t, remote.Close())

	assert.True(t, run.wait(t), "relay must report the cut")
	assert.GreaterOrEqual(t, time.Since(closedAt), bound)
	select {
	case <-writeErr:
	case <-time.After(5 * time.Second):
		t.Fatal("local client kept writing after the relay returned")
	}
}

func TestRelayCutsLocalClientWhoseWriteBlockedBeforeRemoteClosed(t *testing.T) {
	t.Parallel()
	pipeEnd, agentConn := net.Pipe()
	cliConn := newGatedConn(pipeEnd)
	local, remote, run := startStreamRelay(t, cliConn, agentConn, 200*time.Millisecond)
	t.Cleanup(cliConn.unblock)
	cliConn.block()
	_, writeErr := writeUntilError(local)
	<-cliConn.stalled

	require.NoError(t, remote.Close())

	select {
	case <-writeErr:
	case <-time.After(5 * time.Second):
		t.Fatal("relay left the local client open past its bound")
	}
	cliConn.unblock()
	assert.True(t, run.wait(t), "relay must report the cut")
}

func TestRelayCutsLocalClientThatHalfClosedWhenRemoteStaysSilent(t *testing.T) {
	t.Parallel()
	local, remote, run := startPipeStreamRelay(t, 200*time.Millisecond)

	sendAndHalfClose(t, local, remote, "request")

	assert.True(t, run.wait(t), "relay must report the cut")
	require.NoError(t, local.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err := io.ReadAll(local)
	require.Error(t, err, "a cut must reset the local client, not end its stream cleanly")
	assert.NotErrorIs(t, err, os.ErrDeadlineExceeded)
}

func TestRelayCutsQuietLocalClientExactlyAtBound(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		local, remote, run := startPipeRelay(time.Minute)

		require.NoError(t, remote.Close())
		time.Sleep(time.Minute - time.Millisecond)
		synctest.Wait()
		require.False(t, run.finished(), "relay closed before its bound")

		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		require.True(t, run.finished(), "relay outlived its bound")
		assert.True(t, run.cut)
		data, err := io.ReadAll(local)
		require.NoError(t, err)
		assert.Empty(t, data)
	})
}

func TestRelayReportsNoCutWhenBothDirectionsEndBeforeBound(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		local, remote, run := startPipeRelay(time.Minute)

		require.NoError(t, remote.Close())
		time.Sleep(time.Minute - time.Millisecond)
		require.NoError(t, local.Close())
		synctest.Wait()

		require.True(t, run.finished())
		assert.False(t, run.cut)
	})
}

func TestRelayKeepsOpenConnectionPastBoundUntilADirectionEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		local, remote, run := startPipeRelay(time.Minute)

		time.Sleep(2 * time.Minute)
		synctest.Wait()
		require.False(t, run.finished(), "relay closed while both directions were open")

		go func() { _, _ = local.Write([]byte("ping")) }()
		buf := make([]byte, 4)
		_, err := io.ReadFull(remote, buf)
		require.NoError(t, err)
		assert.Equal(t, "ping", string(buf))

		_ = local.Close()
		_ = remote.Close()
		<-run.done
	})
}
