package tunnel

import (
	"errors"
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

var TunnelCmd = &cobra.Command{
	Use:   "tunnel SERVER -l LOCAL_PORT -r REMOTE_PORT [-- COMMAND [ARGS...]]",
	Short: "Create a TCP tunnel, optionally with a local command attached",
	Long: `
	Create a TCP tunnel that forwards local TCP traffic to a remote server port.
	Use -l/--local and -r/--remote to configure local and remote ports.
	If '-- COMMAND [ARGS...]' is provided, Alpacon runs the local command
	in the same session with the tunnel lifecycle attached.

	Use '--' before the local command so Alpacon parses tunnel flags and forwards
	the rest to the local program exactly as provided.
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

	# Run psql with attached tunnel session
	alpacon tunnel prod-db -l 5432 -r 5432 -- psql -h 127.0.0.1 -p 5432 -U app appdb

	# Run kubectl with attached tunnel session
	alpacon tunnel prod-k8s -l 6443 -r 6443 -- kubectl --server=https://127.0.0.1:6443 get pods
	`,
	Args: validateTunnelArgs,
	Run:  runTunnel,
}

func init() {
	bindTunnelFlags(TunnelCmd, &tunnelFlags)
}

func bindTunnelFlags(cmd *cobra.Command, flags *tunnelFlagValues) {
	cmd.Flags().StringVarP(&flags.localPort, "local", "l", "", "Local port to listen on (required)")
	cmd.Flags().StringVarP(&flags.remotePort, "remote", "r", "", "Remote port to connect to (required)")
	cmd.Flags().StringVarP(&flags.username, "username", "u", "", "Username for the tunnel")
	cmd.Flags().StringVarP(&flags.groupname, "groupname", "g", "", "Groupname for the tunnel")
	cmd.Flags().BoolVarP(&flags.verbose, "verbose", "v", false, "Show connection logs")

	if err := cmd.MarkFlagRequired("local"); err != nil {
		panic(err)
	}
	if err := cmd.MarkFlagRequired("remote"); err != nil {
		panic(err)
	}
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

func validateTunnelArgs(cmd *cobra.Command, args []string) error {
	dashIndex := cmd.ArgsLenAtDash()
	if dashIndex >= 0 {
		_, _, err := extractRunInvocation(args, dashIndex)
		return err
	}

	switch len(args) {
	case 1:
		return nil
	case 0:
		return errors.New("server name is required")
	default:
		if args[0] == "run" {
			return errors.New(removedRunSubcommandHint)
		}
		return errors.New(missingRunSeparatorUsage + " " + shellOneLinerHint)
	}
}

func runTunnel(cmd *cobra.Command, args []string) {
	var sigChan <-chan os.Signal
	if cmd.ArgsLenAtDash() < 0 {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(ch)
		sigChan = ch
	}

	exitCode, err := executeTunnelCommand(cmd, args, sigChan)
	if err != nil {
		utils.CliError("%s", err)
		if exitCode == 0 {
			exitCode = 1
		}
		os.Exit(exitCode)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func executeTunnelCommand(cmd *cobra.Command, args []string, sigChan <-chan os.Signal) (int, error) {
	dashIndex := cmd.ArgsLenAtDash()
	if dashIndex >= 0 {
		serverName, localCommand, err := extractRunInvocation(args, dashIndex)
		if err != nil {
			return 1, err
		}
		return executeTunnelRunWithInvocation(serverName, localCommand)
	}

	return 0, executeTunnel(args[0], sigChan)
}

func executeTunnel(serverName string, sigChan <-chan os.Signal) error {
	runtime, err := tunnelruntime.Start(tunnelFlags.toStartOptions(serverName))
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
