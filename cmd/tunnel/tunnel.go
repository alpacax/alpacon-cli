package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
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
	localPort  string
	remotePort string
	username   string
	groupname  string
	verbose    bool
)

// tunnelContext holds shared resources for TCP connection handling.
type tunnelContext struct {
	session      streamSession
	listener     net.Listener
	remotePort   string
	verbose      bool
	done         chan struct{}
	shutdownErr  chan error
	shutdownOnce sync.Once
}

type streamSession interface {
	OpenStream() (*smux.Stream, error)
	Close() error
	CloseChan() <-chan struct{}
}

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

	localPortNum, err := strconv.Atoi(localPort)
	if err != nil {
		utils.CliErrorWithExit("Invalid local port: %s", localPort)
	}
	if localPortNum < 1 || localPortNum > 65535 {
		utils.CliErrorWithExit("Local port must be between 1 and 65535: %d", localPortNum)
	}

	targetPort, err := strconv.Atoi(remotePort)
	if err != nil {
		utils.CliErrorWithExit("Invalid remote port: %s", remotePort)
	}
	if targetPort < 1 || targetPort > 65535 {
		utils.CliErrorWithExit("Remote port must be between 1 and 65535: %d", targetPort)
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %v", err)
	}

	tunnelSession, err := tunnelapi.CreateTunnelSession(alpaconClient, serverName, username, groupname, targetPort)
	if err != nil {
		utils.CliErrorWithExit("Failed to create tunnel session: %s", err)
	}

	// Create TCP listener
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%s", localPort))
	if err != nil {
		utils.CliErrorWithExit("Failed to listen on port %s: %s", localPort, err)
	}
	fmt.Printf("Listening on localhost:%s\n", localPort)

	// Connect to proxy server
	headers := alpaconClient.SetWebsocketHeader()
	wsConn, _, err := websocket.DefaultDialer.Dial(tunnelSession.WebsocketURL, headers)
	if err != nil {
		_ = listener.Close()
		utils.CliErrorWithExit("Failed to connect to proxy server: %s", err)
	}
	defer func() { _ = wsConn.Close() }()

	fmt.Println("Connected to proxy server")

	// Create smux session over WebSocket
	wsNetConn := tunnel.NewWebSocketConn(wsConn)
	session, err := smux.Client(wsNetConn, config.GetSmuxConfig())
	if err != nil {
		_ = listener.Close()
		utils.CliErrorWithExit("Failed to create smux session: %s", err)
	}

	fmt.Println("Multiplexing session established")
	fmt.Printf("Tunnel ready: localhost:%s -> %s:%s\n", localPort, serverName, remotePort)
	fmt.Println("Waiting for connections... (Ctrl+C to exit)")

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Create tunnel context for connection handling
	// shutdownTunnel is the single close path for listener and session.
	ctx := &tunnelContext{
		session:     session,
		listener:    listener,
		remotePort:  remotePort,
		verbose:     verbose,
		done:        make(chan struct{}),
		shutdownErr: make(chan error, 1),
	}
	defer shutdownTunnel(ctx, nil)

	// Accept TCP connections
	go acceptConnections(ctx)

	// Monitor session closure (e.g. keepalive failure)
	go func() {
		<-session.CloseChan()
		shutdownTunnel(ctx, fmt.Errorf("session closed by remote"))
	}()

	// Wait for shutdown signal or tunnel failure
	select {
	case <-sigChan:
		fmt.Println("\nShutting down tunnel...")
		shutdownTunnel(ctx, nil)
	case <-ctx.done:
		select {
		case err := <-ctx.shutdownErr:
			utils.CliErrorWithExit("Tunnel connection lost: %s", err)
		default:
			fmt.Println("\nTunnel connection lost, shutting down...")
		}
	}
	fmt.Println("Tunnel closed.")
}

func acceptConnections(ctx *tunnelContext) {
	var tempDelay time.Duration
	for {
		tcpConn, err := ctx.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if tempDelay > 1*time.Second {
					tempDelay = 1 * time.Second
				}
				utils.CliWarning("Accept error (retrying in %v): %s", tempDelay, err)
				select {
				case <-time.After(tempDelay):
					continue
				case <-ctx.done:
					return
				}
			}
			utils.CliError("Accept error: %s", err)
			shutdownTunnel(ctx, fmt.Errorf("accept failed: %w", err))
			return
		}
		tempDelay = 0
		go handleTCPConnection(tcpConn, ctx)
	}
}

func shutdownTunnel(ctx *tunnelContext, cause error) {
	ctx.shutdownOnce.Do(func() {
		if ctx.listener != nil {
			if err := ctx.listener.Close(); err != nil && ctx.verbose && !errors.Is(err, net.ErrClosed) {
				utils.CliWarning("Failed to close listener: %s", err)
			}
		}
		if ctx.session != nil {
			if err := ctx.session.Close(); err != nil && ctx.verbose {
				utils.CliWarning("Failed to close session: %s", err)
			}
		}
		if cause != nil {
			select {
			case ctx.shutdownErr <- cause:
			default:
			}
		}
		close(ctx.done)
	})
}

// handleTCPConnection handles a single TCP connection.
func handleTCPConnection(tcpConn net.Conn, ctx *tunnelContext) {
	defer func() { _ = tcpConn.Close() }()

	// Create smux stream
	stream, err := ctx.session.OpenStream()
	if err != nil {
		utils.CliError("Failed to open stream: %s", err)
		shutdownTunnel(ctx, fmt.Errorf("open stream failed: %w", err))
		return
	}
	defer func() { _ = stream.Close() }()

	// Send metadata (target port information)
	metadata := map[string]string{"remote_port": ctx.remotePort}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		utils.CliError("Failed to marshal metadata: %s", err)
		return
	}
	metadataBytes = append(metadataBytes, '\n')

	if _, err := stream.Write(metadataBytes); err != nil {
		utils.CliError("Failed to send metadata: %s", err)
		return
	}

	if ctx.verbose {
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
	if ctx.verbose {
		fmt.Printf("Connection closed: %s\n", tcpConn.RemoteAddr())
	}
}
