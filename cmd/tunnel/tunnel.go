package tunnel

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tunnelruntime "github.com/alpacax/alpacon-cli/pkg/tunnel/runtime"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type tunnelFlagValues struct {
	localPort  string
	remotePort string
	username   string
	groupname  string
	verbose    bool
}

type tunnelCommandRuntime interface {
	LocalAddress() string
	RemoteAddress() string
	CheckReady() error
	Done() <-chan struct{}
	Cause() error
	Close(cause error)
}

var tunnelFlags tunnelFlagValues

var tunnelCommandStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
	return tunnelruntime.Start(opts)
}

var TunnelCmd = &cobra.Command{
	Use:   "tunnel SERVER -l LOCAL_PORT -r REMOTE_PORT",
	Short: "Create a TCP tunnel to a remote server",
	Long: `
	Create a TCP tunnel that forwards local TCP traffic to a remote server port.
	Use -l/--local and -r/--remote to configure local and remote ports.
	The tunnel uses WebSocket + smux multiplexing for efficient connection handling.
	`,
	Example: `
	# Forward local 9000 to remote 8082
	alpacon tunnel my-server -l 9000 -r 8082

	# Same using long flags
	alpacon tunnel my-server --local 9000 --remote 8082

	# Forward local 2222 to remote SSH port 22
	alpacon tunnel my-server -l 2222 -r 22

	# Specify username and groupname for the tunnel
	alpacon tunnel my-server -l 9000 -r 8082 -u admin -g developers
	`,
	Args: cobra.ExactArgs(1),
	Run:  runTunnel,
}

func init() {
	bindTunnelFlags(TunnelCmd, &tunnelFlags)

	TunnelCmd.AddCommand(TunnelRunCmd)
}

func bindTunnelFlags(cmd *cobra.Command, flags *tunnelFlagValues) {
	cmd.Flags().StringVarP(&flags.localPort, "local", "l", "", "Local port to listen on (required)")
	cmd.Flags().StringVarP(&flags.remotePort, "remote", "r", "", "Remote port to connect to (required)")
	cmd.Flags().StringVarP(&flags.username, "username", "u", "", "Username for the tunnel")
	cmd.Flags().StringVarP(&flags.groupname, "groupname", "g", "", "Groupname for the tunnel")
	cmd.Flags().BoolVarP(&flags.verbose, "verbose", "v", false, "Show connection logs")

	_ = cmd.MarkFlagRequired("local")
	_ = cmd.MarkFlagRequired("remote")
}

func (f tunnelFlagValues) toStartOptions(serverName string) tunnelruntime.StartOptions {
	return tunnelruntime.StartOptions{
		ServerName: serverName,
		LocalPort:  f.localPort,
		RemotePort: f.remotePort,
		Username:   f.username,
		Groupname:  f.groupname,
		Verbose:    f.verbose,
	}
}

func runTunnel(cmd *cobra.Command, args []string) {
	serverName := args[0]

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	if err := executeTunnel(serverName, sigChan); err != nil {
		utils.CliErrorWithExit("%s", err)
	}
}

func executeTunnel(serverName string, sigChan <-chan os.Signal) error {
	runtime, err := tunnelCommandStarter(tunnelFlags.toStartOptions(serverName))
	if err != nil {
		return err
	}
	defer runtime.Close(nil)
	if err := runtime.CheckReady(); err != nil {
		return fmt.Errorf("failed to establish tunnel connection: %w", err)
	}

	utils.CliInfo("Tunnel ready: %s -> %s", runtime.LocalAddress(), runtime.RemoteAddress())
	utils.CliInfo("Waiting for connections... (Ctrl+C to exit)")

	userRequestedShutdown := false
	select {
	case <-sigChan:
		userRequestedShutdown = true
		utils.CliInfo("Shutting down tunnel...")
		runtime.Close(nil)
		<-runtime.Done()
	case <-runtime.Done():
	}

	if !userRequestedShutdown {
		if err := runtime.Cause(); err != nil {
			return fmt.Errorf("tunnel connection lost: %w", err)
		}
	}

	utils.CliInfo("Tunnel closed")
	return nil
}
