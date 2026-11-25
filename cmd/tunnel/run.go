package tunnel

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	localCommandGracePeriod  = 5 * time.Second
	remoteCloseGracePeriod   = 3 * time.Second
	forceInterruptBufferSize = 2
	missingRunSeparatorUsage = "missing '--' separator. usage: alpacon tunnel SERVER -l <LOCAL_PORT> -r <REMOTE_PORT> -- COMMAND [ARGS...]"
	removedRunSubcommandHint = "`alpacon tunnel run` has been removed. use: alpacon tunnel SERVER -l <LOCAL_PORT> -r <REMOTE_PORT> -- COMMAND [ARGS...]"
	shellOneLinerHint        = "(for shell-style one-liner, use: -- sh -c \"<command>\")"
)

var tunnelSignals = func() (<-chan os.Signal, func()) {
	signals := make(chan os.Signal, forceInterruptBufferSize)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return signals, func() { signal.Stop(signals) }
}

var newLocalCommand = exec.Command

func executeTunnelRunWithInvocation(ios *utils.IOStreams, ac *client.AlpaconClient, flags tunnelFlagValues, serverName string, localCommand []string) (int, error) {
	runtime, err := startTunnel(flags.toStartOptions(ac, serverName))
	if err != nil {
		err = handleTunnelStartError(ac, flags, err, serverName, func() error {
			runtime, err = startTunnel(flags.toStartOptions(ac, serverName))
			return err
		})
		if err != nil {
			return 1, err
		}
	}
	defer func() { // Close is idempotent; monitorLocalCommand has usually closed it already
		runtime.Close(nil)
		<-runtime.Done()
	}()
	if err := runtime.CheckReady(); err != nil {
		return 1, fmt.Errorf("failed to establish tunnel connection: %w", err)
	}
	sigChan, stopSignals := tunnelSignals()
	defer stopSignals()

	fmt.Fprintf(ios.ErrOut, "[alpacon tunnel] CONNECTED %s -> %s\n", runtime.LocalAddress(), runtime.RemoteAddress())
	_, _ = fmt.Fprintln(ios.ErrOut, "[alpacon tunnel] (Ctrl+C: interrupt, Ctrl+C twice: force stop)")

	commandName := localCommand[0]
	commandArgs := localCommand[1:]

	localCmd := newLocalCommand(commandName, commandArgs...)
	localCmd.Stdin = os.Stdin
	localCmd.Stdout = os.Stdout
	localCmd.Stderr = utils.StderrForChild()

	select {
	case <-sigChan:
		ios.Info("Interrupt received. The local command was not started.")
		return 1, nil
	default:
	}
	if err := localCmd.Start(); err != nil {
		return 1, fmt.Errorf("failed to start local command %q: %w", commandName, err)
	}

	exitCode, runtimeErr := monitorLocalCommand(ios, runtime, localCmd, sigChan)
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
		if len(args) > 0 && args[0] == "run" {
			return "", nil, errors.New(removedRunSubcommandHint)
		}
		return "", nil, errors.New("only one server name can be provided before '--'")
	}
	if len(args) <= dashIndex {
		return "", nil, errors.New("local command is required after '--'")
	}

	return args[0], append([]string(nil), args[dashIndex:]...), nil
}

func monitorLocalCommand(ios *utils.IOStreams, runtime tunnelCommandRuntime, localCmd *exec.Cmd, sigChan <-chan os.Signal) (int, error) {
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- localCmd.Wait()
	}()

	interruptCount := 0
	var graceTimer <-chan time.Time

	for {
		select {
		case err := <-waitCh:
			runtime.Close(nil)
			<-runtime.Done()
			return exitCodeFromProcessError(err), nil

		case <-runtime.Done():
			select {
			case err := <-waitCh:
				return exitCodeFromProcessError(err), nil
			default:
			}

			cause := runtime.Cause()
			if cause == nil {
				err := waitForProcessExit(ios, waitCh, localCmd, remoteCloseGracePeriod)
				return exitCodeFromProcessError(err), nil
			}

			ios.Warning("Tunnel closed: %s", cause)
			signalProcess(ios, localCmd, os.Interrupt)
			err := waitForProcessExit(ios, waitCh, localCmd, remoteCloseGracePeriod)
			exitCode := exitCodeFromProcessError(err)
			if exitCode == 0 {
				exitCode = 1
			}
			return exitCode, fmt.Errorf("tunnel connection lost: %w", cause)

		case sig := <-sigChan:
			interruptCount++
			if interruptCount == 1 {
				if sig == os.Interrupt {
					ios.Info("Interrupt received. Stopping local command... (Press Ctrl+C again to force stop)")
					signalProcess(ios, localCmd, os.Interrupt)
				} else {
					ios.Info("Termination signal received. Stopping local command...")
					signalProcess(ios, localCmd, sig)
				}
				graceTimer = time.After(localCommandGracePeriod)
				continue
			}

			ios.Warning("Force stopping local command.")
			killProcess(ios, localCmd)
			runtime.Close(nil)
			<-runtime.Done()
			err := waitForProcessExit(ios, waitCh, localCmd, remoteCloseGracePeriod)
			return exitCodeFromProcessError(err), nil

		case <-graceTimer:
			ios.Warning("Grace period elapsed. Force stopping local command.")
			killProcess(ios, localCmd)
			runtime.Close(nil)
			<-runtime.Done()
			err := waitForProcessExit(ios, waitCh, localCmd, remoteCloseGracePeriod)
			return exitCodeFromProcessError(err), nil
		}
	}
}

func waitForProcessExit(ios *utils.IOStreams, waitCh <-chan error, localCmd *exec.Cmd, timeout time.Duration) error {
	select {
	case err := <-waitCh:
		return err
	case <-time.After(timeout):
		killProcess(ios, localCmd)
	}

	select {
	case err := <-waitCh:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("local command did not exit after force kill")
	}
}

func killProcess(ios *utils.IOStreams, localCmd *exec.Cmd) {
	if localCmd == nil || localCmd.Process == nil {
		return
	}
	if err := localCmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		ios.Warning("Failed to force stop local command: %s", err)
	}
}

func signalProcess(ios *utils.IOStreams, localCmd *exec.Cmd, sig os.Signal) {
	if localCmd == nil || localCmd.Process == nil {
		return
	}
	if err := localCmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		ios.Warning("Failed to signal local command: %s", err)
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
