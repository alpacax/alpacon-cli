package tunnel

import (
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
	username   string
	groupname  string
	verbose    bool
)

var TunnelCmd = &cobra.Command{
	Use:   "tunnel [SERVER NAME]",
	Short: "Create a TCP tunnel to a remote server",
	Long: `
	This command creates a TCP tunnel that forwards local port traffic to a remote server's port.
	The tunnel uses WebSocket + smux multiplexing for efficient connection handling.
	`,
	Example: `
	// Create a tunnel to a remote server
	alpacon tunnel [SERVER NAME] -l [LOCAL PORT] -r [REMOTE PORT]

	// Forward local port 9000 to remote server's port 8082
	alpacon tunnel [SERVER NAME] --local 9000 --remote 8082

	// Forward local port 2222 to remote server's SSH port (22)
	alpacon tunnel [SERVER NAME] -l 2222 -r 22

	// Specify username and groupname for the tunnel
	alpacon tunnel [SERVER NAME] -l [LOCAL PORT] -r [REMOTE PORT] -u [USER NAME] -g [GROUP NAME]

	Flags:
	-l, --local [PORT]                 Local port to listen on (required).
	-r, --remote [PORT]                Remote port to connect to (required).
	-u, --username [USER NAME]         Username for the tunnel.
	-g, --groupname [GROUP NAME]       Groupname for the tunnel.
	-v, --verbose                      Show connection logs.
	`,
	Args: cobra.ExactArgs(1),
	Run:  runTunnel,
}

func init() {
	TunnelCmd.Flags().StringVarP(&localPort, "local", "l", "", "Local port to listen on (required)")
	TunnelCmd.Flags().StringVarP(&remotePort, "remote", "r", "", "Remote port to connect to (required)")
	TunnelCmd.Flags().StringVarP(&username, "username", "u", "", "Username for the tunnel")
	TunnelCmd.Flags().StringVarP(&groupname, "groupname", "g", "", "Groupname for the tunnel")
	TunnelCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show connection logs")

	_ = TunnelCmd.MarkFlagRequired("local")
	_ = TunnelCmd.MarkFlagRequired("remote")
}

func runTunnel(cmd *cobra.Command, args []string) {
	serverName := args[0]

	if localPort == "" || remotePort == "" {
		utils.CliError("Both --local and --remote flags are required")
	}

	targetPort, err := strconv.Atoi(remotePort)
	if err != nil {
		utils.CliError("Invalid remote port: %s", remotePort)
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliError("Failed to connect to Alpacon API: %s", err)
	}

	tunnelSession, err := tunnelapi.CreateTunnelSession(alpaconClient, serverName, username, groupname, targetPort)
	if err != nil {
		utils.CliError("Failed to create tunnel session: %s", err)
	}

	// Create TCP listener
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%s", localPort))
	if err != nil {
		utils.CliError("Failed to listen on port %s: %s", localPort, err)
	}
	defer listener.Close()
	fmt.Printf("Listening on localhost:%s\n", localPort)

	// Connect to proxy server
	headers := alpaconClient.SetWebsocketHeader()
	wsConn, _, err := websocket.DefaultDialer.Dial(tunnelSession.WebsocketURL, headers)
	if err != nil {
		utils.CliError("Failed to connect to proxy server: %s", err)
	}
	defer wsConn.Close()

	fmt.Println("Connected to proxy server")

	// Create smux session over WebSocket
	wsNetConn := tunnel.NewWebSocketConn(wsConn)
	session, err := smux.Client(wsNetConn, config.GetSmuxConfig())
	if err != nil {
		utils.CliError("Failed to create smux session: %s", err)
	}
	defer session.Close()

	fmt.Println("Multiplexing session established")
	fmt.Printf("Tunnel ready: localhost:%s -> %s:%s\n", localPort, serverName, remotePort)
	fmt.Println("Waiting for connections... (Ctrl+C to exit)")

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
			go handleTCPConnection(tcpConn, session, remotePort, verbose, listener)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\nShutting down tunnel...")
	fmt.Println("Tunnel closed.")
}

// handleTCPConnection handles a single TCP connection.
func handleTCPConnection(tcpConn net.Conn, session *smux.Session, remotePort string, verbose bool, listener net.Listener) {
	defer tcpConn.Close()

	// Create smux stream
	stream, err := session.OpenStream()
	if err != nil {
		fmt.Printf("Failed to open stream: %s\n", err)
		listener.Close() // Stop accepting new connections
		return
	}
	defer stream.Close()

	// Send metadata (target port information)
	metadata := map[string]string{"remote_port": remotePort}
	metadataBytes, _ := json.Marshal(metadata)
	metadataBytes = append(metadataBytes, '\n')

	if _, err := stream.Write(metadataBytes); err != nil {
		fmt.Printf("Failed to send metadata: %s\n", err)
		return
	}

	if verbose {
		fmt.Printf("New connection from %s\n", tcpConn.RemoteAddr())
	}

	// Bidirectional relay using buffer pool
	errChan := make(chan error, 2)

	// TCP -> smux stream
	go func() {
		_, err := tunnel.CopyBuffered(stream, tcpConn)
		errChan <- err
	}()

	// smux stream -> TCP
	go func() {
		_, err := tunnel.CopyBuffered(tcpConn, stream)
		errChan <- err
	}()

	// Wait for one direction to complete
	<-errChan
	if verbose {
		fmt.Printf("Connection closed: %s\n", tcpConn.RemoteAddr())
	}
}
