package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	tunnelapi "github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	basetunnel "github.com/alpacax/alpacon-cli/pkg/tunnel"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"
)

const smuxHeaderSize = 8 // version, command, length, stream id

const openStatusTimeout = 5 * time.Second // bounds the lookup that explains a failed dial

// StartOptions contains user-provided inputs for starting a tunnel runtime.
type StartOptions struct {
	ServerName    string
	LocalPort     string // Use "0" to auto-assign the local port.
	RemotePort    string
	Username      string
	Groupname     string
	Verbose       bool
	WorkSessionID string // Optional work-session UUID to attach to the tunnel.
}

type streamSession interface {
	OpenStream() (*smux.Stream, error)
	Close() error
	CloseChan() <-chan struct{}
}

// Runtime owns tunnel lifecycle resources (listener, smux session, websocket).
type Runtime struct {
	session    streamSession
	listener   net.Listener
	wsConn     io.Closer
	serverName string
	remotePort string
	verbose    bool
	localPort  int

	done         chan struct{}
	shutdownOnce sync.Once

	causeMu sync.RWMutex
	cause   error
}

// Start initializes a TCP tunnel runtime and starts accepting local TCP connections.
func Start(opts StartOptions) (*Runtime, error) {
	if opts.ServerName == "" {
		return nil, errors.New("server name is required")
	}

	targetPort, err := parsePort(opts.RemotePort, false)
	if err != nil {
		return nil, fmt.Errorf("invalid remote port: %w", err)
	}

	bindPort := opts.LocalPort
	if bindPort == "" {
		bindPort = "0"
	}
	if _, err := parsePort(bindPort, true); err != nil {
		return nil, fmt.Errorf("invalid local port: %w", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%s", bindPort))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on local port: %w", err)
	}
	resolvedLocalPort, err := extractTCPPort(listener.Addr())
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("failed to resolve local port: %w", err)
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("connection to Alpacon API failed: %w", err)
	}

	tunnelSession, err := tunnelapi.CreateTunnelSession(alpaconClient, opts.ServerName, opts.Username, opts.Groupname, targetPort, opts.WorkSessionID)
	if err != nil {
		_ = listener.Close()
		return nil, utils.MarkSubmission(fmt.Errorf("failed to create tunnel session: %w", err))
	}

	// The tunnel session exists from here on; what fails after it is returned
	// untagged, so a refused dial never reads as a refused create.
	smuxConfig := config.GetSmuxConfig()
	wsConn, err := dialTunnel(alpaconClient, tunnelSession, opts.ServerName, smuxConfig.MaxFrameSize, openStatusTimeout)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	session, err := smux.Client(basetunnel.NewWebSocketConn(wsConn), smuxConfig)
	if err != nil {
		_ = listener.Close()
		_ = wsConn.Close()
		return nil, fmt.Errorf("failed to create smux session: %w", err)
	}

	runtime := &Runtime{
		session:    session,
		listener:   listener,
		wsConn:     wsConn,
		serverName: opts.ServerName,
		remotePort: opts.RemotePort,
		verbose:    opts.Verbose,
		localPort:  resolvedLocalPort,
		done:       make(chan struct{}),
	}

	go runtime.acceptConnections()
	go func() {
		<-session.CloseChan()
		runtime.shutdown(fmt.Errorf("session closed by remote"))
	}()

	return runtime, nil
}

// CheckReady validates that the tunnel session can open a stream and send metadata.
// This provides a fast fail signal before running long-lived local commands.
func (r *Runtime) CheckReady() error {
	select {
	case <-r.done:
		if cause := r.Cause(); cause != nil {
			return fmt.Errorf("tunnel already closed: %w", cause)
		}
		return errors.New("tunnel already closed")
	default:
	}

	stream, err := r.session.OpenStream()
	if err != nil {
		return fmt.Errorf("failed to open readiness stream: %w", err)
	}
	defer func() { _ = stream.Close() }()

	metadataBytes, err := buildTunnelMetadata(r.remotePort)
	if err != nil {
		return fmt.Errorf("failed to build readiness metadata: %w", err)
	}

	if _, err := stream.Write(metadataBytes); err != nil {
		return fmt.Errorf("failed to send readiness metadata: %w", err)
	}

	return nil
}

// LocalPort returns the resolved localhost port.
func (r *Runtime) LocalPort() int {
	return r.localPort
}

// LocalAddress returns the resolved localhost bind address.
func (r *Runtime) LocalAddress() string {
	return fmt.Sprintf("127.0.0.1:%d", r.localPort)
}

// RemoteAddress returns "<serverName>:<remotePort>".
func (r *Runtime) RemoteAddress() string {
	return fmt.Sprintf("%s:%s", r.serverName, r.remotePort)
}

// Done returns a channel closed when runtime shutdown completes.
func (r *Runtime) Done() <-chan struct{} {
	return r.done
}

// Cause returns the first non-nil shutdown cause, if any.
func (r *Runtime) Cause() error {
	r.causeMu.RLock()
	defer r.causeMu.RUnlock()
	return r.cause
}

// Close initiates runtime shutdown.
func (r *Runtime) Close(cause error) {
	r.shutdown(cause)
}

func (r *Runtime) shutdown(cause error) {
	r.shutdownOnce.Do(func() {
		r.setCause(cause)

		if r.listener != nil {
			if err := r.listener.Close(); err != nil && r.verbose && !errors.Is(err, net.ErrClosed) {
				utils.CliWarning("Failed to close listener: %s", err)
			}
		}
		if r.session != nil {
			if err := r.session.Close(); err != nil && r.verbose {
				utils.CliWarning("Failed to close session: %s", err)
			}
		}
		if r.wsConn != nil {
			if err := r.wsConn.Close(); err != nil && r.verbose {
				utils.CliWarning("Failed to close WebSocket connection: %s", err)
			}
		}

		close(r.done)
	})
}

func (r *Runtime) setCause(cause error) {
	if cause == nil {
		return
	}

	r.causeMu.Lock()
	defer r.causeMu.Unlock()
	if r.cause == nil {
		r.cause = cause
	}
}

func (r *Runtime) acceptConnections() {
	var tempDelay time.Duration
	for {
		tcpConn, err := r.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}

			if isRetryableAcceptError(err) {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				tempDelay = min(tempDelay, time.Second)
				if r.verbose {
					utils.CliWarning("Failed to accept connection (retrying in %v): %s", tempDelay, err)
				}
				select {
				case <-time.After(tempDelay):
					continue
				case <-r.done:
					return
				}
			}

			r.shutdown(fmt.Errorf("accept failed: %w", err))
			return
		}

		tempDelay = 0
		go r.handleTCPConnection(tcpConn)
	}
}

func (r *Runtime) handleTCPConnection(tcpConn net.Conn) {
	defer func() { _ = tcpConn.Close() }()

	stream, err := r.session.OpenStream()
	if err != nil {
		r.shutdown(fmt.Errorf("open stream failed: %w", err))
		return
	}
	defer func() { _ = stream.Close() }()

	metadataBytes, err := buildTunnelMetadata(r.remotePort)
	if err != nil {
		utils.CliWarning("Failed to marshal metadata: %s", err)
		return
	}

	if _, err := stream.Write(metadataBytes); err != nil {
		utils.CliWarning("Failed to send metadata: %s", err)
		return
	}

	if r.verbose {
		utils.CliInfo("Connection opened: %s", tcpConn.RemoteAddr())
	}

	errChan := make(chan error, 2)

	go func() {
		_, err := basetunnel.CopyBuffered(stream, tcpConn)
		errChan <- err
	}()

	go func() {
		_, err := basetunnel.CopyBuffered(tcpConn, stream)
		errChan <- err
	}()

	<-errChan
	if r.verbose {
		utils.CliInfo("Connection closed: %s", tcpConn.RemoteAddr())
	}
}

func newTunnelDialer(maxFrameSize int) *websocket.Dialer {
	return &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: websocket.DefaultDialer.HandshakeTimeout,
		WriteBufferSize:  smuxHeaderSize + maxFrameSize,
	}
}

func parsePort(value string, allowAuto bool) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("port %q is not a number", value)
	}

	if allowAuto && port == 0 {
		return port, nil
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535")
	}

	return port, nil
}

func buildTunnelMetadata(remotePort string) ([]byte, error) {
	metadata := map[string]string{"remote_port": remotePort}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return append(metadataBytes, '\n'), nil
}

func extractTCPPort(addr net.Addr) (int, error) {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok || tcpAddr == nil {
		return 0, fmt.Errorf("non-TCP listener address: %T", addr)
	}
	return tcpAddr.Port, nil
}

func isRetryableAcceptError(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// dialTunnel connects to the tunnel proxy and, when that fails, asks the session why it did not open.
func dialTunnel(ac *client.AlpaconClient, tunnelSession *tunnelapi.TunnelSessionResponse, serverName string, maxFrameSize int, lookupTimeout time.Duration) (*websocket.Conn, error) {
	wsConn, resp, err := newTunnelDialer(maxFrameSize).Dial(tunnelSession.WebsocketURL, ac.SetWebsocketHeader())
	if err == nil {
		return wsConn, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	detail, lookupErr := tunnelapi.GetTunnelSession(ctx, ac, tunnelSession.ID)
	if lookupErr != nil {
		utils.CliDebug("could not read why the tunnel session did not open: %v", utils.RedactURLError(lookupErr))
	}
	return nil, explainDialFailure(serverName, err, resp, detail)
}

// explainDialFailure reports why the tunnel did not open when the session detail says so,
// and otherwise the dial error with its handshake status.
func explainDialFailure(serverName string, dialErr error, resp *http.Response, detail *tunnelapi.TunnelSessionDetail) error {
	if detail != nil {
		switch detail.OpenStatus {
		case tunnelapi.OpenStatusFailed:
			if reason := oneLineReason(detail.OpenFailure.Result); reason != "" {
				return fmt.Errorf("failed to open tunnel on %s: %s", serverName, reason)
			}
			return fmt.Errorf("failed to open tunnel on %s", serverName)
		case tunnelapi.OpenStatusPending:
			if resp != nil && resp.StatusCode == http.StatusServiceUnavailable {
				return fmt.Errorf("failed to open tunnel on %s: the agent did not respond to the tunnel request", serverName)
			}
		}
	}
	return utils.WebsocketDialError("failed to connect to proxy server", dialErr, resp)
}

// oneLineReason returns agent text as one line. Whitespace becomes a space before the
// sanitizer runs, which would otherwise drop a tab or line break and join the words around it.
func oneLineReason(result string) string {
	spaced := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, result)
	return strings.Join(strings.Fields(utils.SanitizeTerminalText(spaced)), " ")
}
