package tunnel

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

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
	localPort  string
	remotePort string
	proxyURL   string
	protocol   string
)

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
		session, err := tunnelapi.CreateTunnelSession(alpaconClient, serverName, protocol, targetPort)
		if err != nil {
			utils.CliError("Failed to create tunnel session: %s", err)
		}
		tunnelProxyURL = session.WebsocketURL
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
	fmt.Println("🔄 Waiting for connections... (Ctrl+C to exit)")

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
			go handleTCPConnection(tcpConn, session, remotePort)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\n🛑 Shutting down tunnel...")
	fmt.Println("✅ Tunnel closed.")
}

// handleTCPConnection handles a single TCP connection by creating a smux stream
// and relaying data bidirectionally.
func handleTCPConnection(tcpConn net.Conn, session *smux.Session, remotePort string) {
	defer tcpConn.Close()

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

	fmt.Printf("🔗 New connection from %s\n", tcpConn.RemoteAddr())

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
	fmt.Printf("🏁 Connection closed: %s\n", tcpConn.RemoteAddr())
}

