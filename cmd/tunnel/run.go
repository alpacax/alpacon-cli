package tunnel

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	tunnelruntime "github.com/alpacax/alpacon-cli/pkg/tunnel/runtime"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const (
	localCommandGracePeriod  = 5 * time.Second
	remoteCloseGracePeriod   = 3 * time.Second
	forceInterruptBufferSize = 2
	missingRunSeparatorUsage = "missing '--' separator. usage: alpacon tunnel run SERVER -l <LOCAL_PORT> -r <REMOTE_PORT> -- COMMAND [ARGS...]"
	shellOneLinerHint        = "(for shell-style one-liner, use: -- sh -c \"<command>\")"
)

var runFlags tunnelFlagValues

var (
	runTunnelStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return tunnelruntime.Start(opts)
	}
	runLocalCommandFactory = exec.Command
)

var TunnelRunCmd = &cobra.Command{
	Use:   "run SERVER -l LOCAL_PORT -r REMOTE_PORT -- COMMAND [ARGS...]",
	Short: "Run a local TCP program with an attached tunnel session",
	Long: `Create a TCP tunnel and execute a local command in the same terminal session.

Use '--' before the local command so Alpacon parses tunnel flags and forwards the rest to the local program.
This is preferred because arguments are preserved exactly.

Alpacon does not auto-detect application ports. Pass your app target explicitly
(for example, 127.0.0.1:<LOCAL_PORT>).`,
	Example: `
	# Run psql through tunnel
	alpacon tunnel run prod-db -l 5432 -r 5432 -- psql -h 127.0.0.1 -p 5432 -U app appdb

	# Run kubectl through tunnel
	alpacon tunnel run prod-k8s -l 6443 -r 6443 -- kubectl --server=https://127.0.0.1:6443 get pods

	# Run SSH through tunnel
	alpacon tunnel run bastion -l 2222 -r 22 -- ssh -p 2222 user@127.0.0.1
	`,
	Args: validateTunnelRunArgs,
	Run:  runTunnelWithLocalCommand,
}

func init() {
	bindTunnelFlags(TunnelRunCmd, &runFlags)
}

func validateTunnelRunArgs(cmd *cobra.Command, args []string) error {
	_, _, err := extractRunInvocation(args, cmd.ArgsLenAtDash())
	return err
}

func runTunnelWithLocalCommand(cmd *cobra.Command, args []string) {
	exitCode, err := executeTunnelRun(cmd, args)
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

func executeTunnelRun(cmd *cobra.Command, args []string) (int, error) {
	serverName, localCommand, err := extractRunInvocation(args, cmd.ArgsLenAtDash())
	if err != nil {
		return 1, err
	}

	return executeTunnelRunWithInvocation(serverName, localCommand)
}

func executeTunnelRunWithInvocation(serverName string, localCommand []string) (int, error) {
	runtime, err := runTunnelStarter(runFlags.toStartOptions(serverName))
	if err != nil {
		return 1, err
	}
	if err := runtime.CheckReady(); err != nil {
		runtime.Close(nil)
		<-runtime.Done()
		return 1, fmt.Errorf("failed to establish tunnel connection: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[alpacon tunnel] CONNECTED %s -> %s\n", runtime.LocalAddress(), runtime.RemoteAddress())
	fmt.Fprintln(os.Stderr, "[alpacon tunnel] streams=0 status=healthy (Ctrl+C: interrupt, Ctrl+C twice: force stop)")

	commandName := localCommand[0]
	commandArgs := localCommand[1:]

	localCmd := runLocalCommandFactory(commandName, commandArgs...)
	localCmd.Stdin = os.Stdin
	localCmd.Stdout = os.Stdout
	localCmd.Stderr = os.Stderr
	localCmd.Env = os.Environ()
	localCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := localCmd.Start(); err != nil {
		runtime.Close(nil)
		<-runtime.Done()
		return 1, fmt.Errorf("failed to start local command %q: %w", commandName, err)
	}

	exitCode, runtimeErr := monitorLocalCommand(runtime, localCmd)
	if runtimeErr != nil {
		return exitCode, runtimeErr
	}
	return exitCode, nil
}

func extractRunInvocation(args []string, dashIndex int) (string, []string, error) {
	if dashIndex < 0 {
		return "", nil, errors.New(missingRunSeparatorUsage + " " + shellOneLinerHint)
	}
	if dashIndex == 0 {
		return "", nil, errors.New("server name is required before '--'")
	}
	if dashIndex > 1 {
		return "", nil, errors.New("only one server name can be provided before '--'")
	}
	if len(args) <= dashIndex {
		return "", nil, errors.New("local command is required after '--'")
	}

	return args[0], append([]string(nil), args[dashIndex:]...), nil
}

func monitorLocalCommand(runtime tunnelCommandRuntime, localCmd *exec.Cmd) (int, error) {
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- localCmd.Wait()
	}()

	sigChan := make(chan os.Signal, forceInterruptBufferSize)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	interruptCount := 0
	var graceTimer <-chan time.Time

	for {
		select {
		case err := <-waitCh:
			runtime.Close(nil)
			<-runtime.Done()
			return exitCodeFromProcessError(err), nil

		case <-runtime.Done():
			cause := runtime.Cause()
			if cause == nil {
				err := waitForProcessExit(waitCh, localCmd, remoteCloseGracePeriod)
				return exitCodeFromProcessError(err), nil
			}

			utils.CliWarning("Tunnel closed: %s", cause)
			interruptProcess(localCmd)
			err := waitForProcessExit(waitCh, localCmd, remoteCloseGracePeriod)
			exitCode := exitCodeFromProcessError(err)
			if exitCode == 0 {
				exitCode = 1
			}
			return exitCode, fmt.Errorf("tunnel connection lost: %w", cause)

		case <-sigChan:
			interruptCount++
			if interruptCount == 1 {
				utils.CliInfo("Interrupt received. Stopping local command... (Press Ctrl+C again to force stop)")
				interruptProcess(localCmd)
				graceTimer = time.After(localCommandGracePeriod)
				continue
			}

			utils.CliWarning("Force stopping local command.")
			killProcess(localCmd)
			runtime.Close(nil)
			<-runtime.Done()
			err := <-waitCh
			return exitCodeFromProcessError(err), nil

		case <-graceTimer:
			utils.CliWarning("Grace period elapsed. Force stopping local command.")
			killProcess(localCmd)
			runtime.Close(nil)
			<-runtime.Done()
			err := <-waitCh
			return exitCodeFromProcessError(err), nil
		}
	}
}

func waitForProcessExit(waitCh <-chan error, localCmd *exec.Cmd, timeout time.Duration) error {
	select {
	case err := <-waitCh:
		return err
	case <-time.After(timeout):
		killProcess(localCmd)
		return <-waitCh
	}
}

func interruptProcess(localCmd *exec.Cmd) {
	signalProcess(localCmd, os.Interrupt)
}

func killProcess(localCmd *exec.Cmd) {
	if localCmd == nil || localCmd.Process == nil {
		return
	}
	if localCmd.Process.Pid > 0 {
		if err := syscall.Kill(-localCmd.Process.Pid, syscall.SIGKILL); err == nil || errors.Is(err, syscall.ESRCH) {
			return
		}
	}
	if err := localCmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		utils.CliWarning("Failed to force stop local command: %s", err)
	}
}

func signalProcess(localCmd *exec.Cmd, sig os.Signal) {
	if localCmd == nil || localCmd.Process == nil {
		return
	}
	if unixSignal, ok := sig.(syscall.Signal); ok && localCmd.Process.Pid > 0 {
		if err := syscall.Kill(-localCmd.Process.Pid, unixSignal); err == nil || errors.Is(err, syscall.ESRCH) {
			return
		}
	}
	if err := localCmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		utils.CliWarning("Failed to signal local command: %s", err)
	}
}

func exitCodeFromProcessError(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}
