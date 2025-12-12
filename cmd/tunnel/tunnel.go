package tunnel

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tunnelapi "github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/tunnel"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/xtaci/smux"
)

var (
	localPort      string
	remotePort     string
	proxyURL       string
	protocol       string
	username       string
	groupname      string
	maxConnections int
	rateLimit      int
)

// connectionLimiter manages concurrent connections and rate limiting
type connectionLimiter struct {
	maxConns    int
	ratePerSec  int
	activeConns int32
	mu          sync.Mutex
	tokens      float64
	lastRefill  time.Time
}

func newConnectionLimiter(maxConns, ratePerSec int) *connectionLimiter {
	return &connectionLimiter{
		maxConns:   maxConns,
		ratePerSec: ratePerSec,
		tokens:     float64(ratePerSec),
		lastRefill: time.Now(),
	}
}

// tryAcquire checks max connections, returns false if limit exceeded
func (l *connectionLimiter) tryAcquire() bool {
	if int(atomic.LoadInt32(&l.activeConns)) >= l.maxConns {
		return false
	}
	atomic.AddInt32(&l.activeConns, 1)
	return true
}

// waitForRateLimit waits until rate limit allows, returns wait duration
func (l *connectionLimiter) waitForRateLimit() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	l.tokens += elapsed * float64(l.ratePerSec)
	if l.tokens > float64(l.ratePerSec) {
		l.tokens = float64(l.ratePerSec)
	}
	l.lastRefill = now

	// If we have tokens, consume one immediately
	if l.tokens >= 1 {
		l.tokens--
		return 0
	}

	// Calculate wait time until next token
	waitTime := time.Duration((1-l.tokens)/float64(l.ratePerSec)*1000) * time.Millisecond
	l.tokens = 0
	return waitTime
}

func (l *connectionLimiter) release() {
	atomic.AddInt32(&l.activeConns, -1)
}

func (l *connectionLimiter) getActiveCount() int32 {
	return atomic.LoadInt32(&l.activeConns)
}

var TunnelCmd = &cobra.Command{
	Use:   "tunnel [SERVER_NAME]",
	Short: "Create a TCP tunnel to a remote server",
	Long: `
	Create a TCP tunnel that forwards local port traffic to a remote server's port.
	The tunnel uses WebSocket + smux multiplexing for efficient connection handling.

	Architecture:
	  [User] → [localhost:local_port] → [WS+smux] → [Proxy Server] → [WS+smux] → [Agent:remote_port]
	`,
	Example: `
	# Forward local port 9000 to remote server's port 8082
	alpacon tunnel my-server --local 9000 --remote 8082

	# Forward local port 2222 to remote server's SSH port (22)
	alpacon tunnel my-server -l 2222 -r 22

	# Use a specific proxy URL instead of auto-discovery
	alpacon tunnel my-server -l 9000 -r 8082 --proxy wss://proxy.example.com/tunnel/client/session123/
	`,
	Args: cobra.ExactArgs(1),
	Run:  runTunnel,
}

func init() {
	TunnelCmd.Flags().StringVarP(&localPort, "local", "l", "", "Local port to listen on (required)")
	TunnelCmd.Flags().StringVarP(&remotePort, "remote", "r", "", "Remote port to connect to (required)")
	TunnelCmd.Flags().StringVar(&proxyURL, "proxy", "", "Proxy server WebSocket URL (optional, auto-discovered if not provided)")
	TunnelCmd.Flags().StringVarP(&protocol, "protocol", "p", "tcp", "Protocol type (tcp, ssh, vnc, rdp, postgresql, mysql)")
	TunnelCmd.Flags().StringVarP(&username, "username", "u", "root", "Username for the tunnel")
	TunnelCmd.Flags().StringVarP(&groupname, "groupname", "g", "", "Groupname for the tunnel")
	TunnelCmd.Flags().IntVar(&maxConnections, "max-connections", 100, "Maximum concurrent connections")
	TunnelCmd.Flags().IntVar(&rateLimit, "rate-limit", 20, "Maximum new connections per second (excess connections are queued)")

	_ = TunnelCmd.MarkFlagRequired("local")
	_ = TunnelCmd.MarkFlagRequired("remote")
}

func runTunnel(cmd *cobra.Command, args []string) {
	serverName := args[0]

	// Validate ports
	if localPort == "" || remotePort == "" {
		utils.CliError("Both --local and --remote flags are required")
	}

	// Parse remote port as integer
	targetPort, err := strconv.Atoi(remotePort)
	if err != nil {
		utils.CliError("Invalid remote port: %s", remotePort)
	}

	// Create API client
	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliError("Failed to connect to Alpacon API: %s", err)
	}

	// Get proxy URL
	tunnelProxyURL := proxyURL
	if tunnelProxyURL == "" {
		// Create tunnel session via API
		session, err := tunnelapi.CreateTunnelSession(alpaconClient, serverName, protocol, username, groupname, targetPort)
		if err != nil {
			utils.CliError("Failed to create tunnel session: %s", err)
		}
		tunnelProxyURL = session.ConnectURL
	}

	// Create TCP listener
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%s", localPort))
	if err != nil {
		utils.CliError("Failed to listen on port %s: %s", localPort, err)
	}
	defer listener.Close()

	fmt.Printf("✅ Listening on localhost:%s\n", localPort)

	// Load TLS config
	cfg, _ := config.LoadConfig()
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.Insecure,
	}

	// Create WebSocket dialer
	dialer := websocket.Dialer{
		TLSClientConfig: tlsConfig,
	}

	// Set headers for authentication (use client's header method)
	headers := alpaconClient.SetWebsocketHeader()

	// Connect to proxy server
	wsConn, _, err := dialer.Dial(tunnelProxyURL, headers)
	if err != nil {
		utils.CliError("Failed to connect to proxy server: %s", err)
	}
	defer wsConn.Close()

	fmt.Printf("✅ Connected to proxy server\n")

	// Create smux session over WebSocket
	wsNetConn := tunnel.NewWebSocketConn(wsConn)
	session, err := smux.Client(wsNetConn, tunnel.GetSmuxConfig())
	if err != nil {
		utils.CliError("Failed to create smux session: %s", err)
	}
	defer session.Close()

	fmt.Println("✅ Multiplexing session established")
	fmt.Printf("🎯 Tunnel ready: localhost:%s → %s:%s\n", localPort, serverName, remotePort)
	fmt.Printf("⚙️  Limits: max %d connections, %d/sec rate limit\n", maxConnections, rateLimit)
	fmt.Println("🔄 Waiting for connections... (Ctrl+C to exit)")

	// Initialize connection limiter
	limiter := newConnectionLimiter(maxConnections, rateLimit)

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Accept TCP connections
	go func() {
		for {
			tcpConn, err := listener.Accept()
			if err != nil {
				return
			}

			// Check max connections limit (reject if exceeded)
			if !limiter.tryAcquire() {
				fmt.Printf("⚠️  Connection rejected (active: %d, max: %d)\n", limiter.getActiveCount(), maxConnections)
				tcpConn.Close()
				continue
			}

			// Apply rate limiting (queue/delay if exceeded)
			if waitTime := limiter.waitForRateLimit(); waitTime > 0 {
				fmt.Printf("⏳ Connection queued, waiting %v...\n", waitTime)
				time.Sleep(waitTime)
			}

			go handleTCPConnectionWithLimiter(tcpConn, session, remotePort, limiter)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\n🛑 Shutting down tunnel...")
	fmt.Println("✅ Tunnel closed.")
}

// handleTCPConnectionWithLimiter handles a single TCP connection with connection limiting.
func handleTCPConnectionWithLimiter(tcpConn net.Conn, session *smux.Session, remotePort string, limiter *connectionLimiter) {
	defer func() {
		tcpConn.Close()
		limiter.release()
	}()

	// Create smux stream
	stream, err := session.OpenStream()
	if err != nil {
		fmt.Printf("❌ Failed to open stream: %s\n", err)
		return
	}
	defer stream.Close()

	// Send metadata (target port information)
	metadata := map[string]string{"remote_port": remotePort}
	metadataBytes, _ := json.Marshal(metadata)
	metadataBytes = append(metadataBytes, '\n')

	if _, err := stream.Write(metadataBytes); err != nil {
		fmt.Printf("❌ Failed to send metadata: %s\n", err)
		return
	}

	fmt.Printf("🔗 New connection from %s (active: %d)\n", tcpConn.RemoteAddr(), limiter.getActiveCount())

	// Bidirectional relay using buffer pool
	errChan := make(chan error, 2)

	// TCP → smux stream
	go func() {
		_, err := tunnel.CopyBuffered(stream, tcpConn)
		errChan <- err
	}()

	// smux stream → TCP
	go func() {
		_, err := tunnel.CopyBuffered(tcpConn, stream)
		errChan <- err
	}()

	// Wait for one direction to complete
	<-errChan
	fmt.Printf("🏁 Connection closed: %s (active: %d)\n", tcpConn.RemoteAddr(), limiter.getActiveCount()-1)
}

