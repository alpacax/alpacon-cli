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
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	localCommandGracePeriod  = 5 * time.Second
	remoteCloseGracePeriod   = 3 * time.Second
	forceInterruptBufferSize = 2
	missingRunSeparatorUsage = "missing '--' separator. usage: alpacon tunnel SERVER -l <LOCAL_PORT> -r <REMOTE_PORT> -- COMMAND [ARGS...]"
	removedRunSubcommandHint = "`alpacon tunnel run` has been removed. use: alpacon tunnel SERVER -l <LOCAL_PORT> -r <REMOTE_PORT> -- COMMAND [ARGS...]"
	shellOneLinerHint        = "(for shell-style one-liner, use: -- sh -c \"<command>\")"
)

func executeTunnelRunWithInvocation(serverName string, localCommand []string) (int, error) {
	runtime, err := tunnelruntime.Start(tunnelFlags.toStartOptions(serverName))
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

	localCmd := exec.Command(commandName, commandArgs...)
	localCmd.Stdin = os.Stdin
	localCmd.Stdout = os.Stdout
	localCmd.Stderr = os.Stderr
	localCmd.Env = os.Environ()
	configureCommandProcess(localCmd)

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

func monitorLocalCommand(runtime tunnelCommandRuntime, localCmd *exec.Cmd) (int, error) {
	defer restoreTerminalForeground()

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
			select {
			case err := <-waitCh:
				return exitCodeFromProcessError(err), nil
			default:
			}

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

		case sig := <-sigChan:
			interruptCount++
			if interruptCount == 1 {
				if sig == os.Interrupt {
					utils.CliInfo("Interrupt received. Stopping local command... (Press Ctrl+C again to force stop)")
					interruptProcess(localCmd)
				} else {
					utils.CliInfo("Termination signal received. Stopping local command...")
					signalProcess(localCmd, sig)
				}
				graceTimer = time.After(localCommandGracePeriod)
				continue
			}

			utils.CliWarning("Force stopping local command.")
			killProcess(localCmd)
			runtime.Close(nil)
			<-runtime.Done()
			err := waitForProcessExit(waitCh, localCmd, remoteCloseGracePeriod)
			return exitCodeFromProcessError(err), nil

		case <-graceTimer:
			utils.CliWarning("Grace period elapsed. Force stopping local command.")
			killProcess(localCmd)
			runtime.Close(nil)
			<-runtime.Done()
			err := waitForProcessExit(waitCh, localCmd, remoteCloseGracePeriod)
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
	}

	select {
	case err := <-waitCh:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("local command did not exit after force kill")
	}
}

func interruptProcess(localCmd *exec.Cmd) {
	signalProcess(localCmd, os.Interrupt)
}

func killProcess(localCmd *exec.Cmd) {
	if localCmd == nil || localCmd.Process == nil {
		return
	}
	if err := killCommandProcess(localCmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
		utils.CliWarning("Failed to force stop local command: %s", err)
	}
}

func signalProcess(localCmd *exec.Cmd, sig os.Signal) {
	if localCmd == nil || localCmd.Process == nil {
		return
	}
	if err := signalCommandProcess(localCmd, sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
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

func configureCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	stdinFd := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFd) {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid:    true,
			Foreground: true,
			Ctty:       stdinFd,
		}
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

func restoreTerminalForeground() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, syscall.Getpgrp())
}

func signalCommandProcess(cmd *exec.Cmd, sig os.Signal) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}

	unixSig, ok := sig.(syscall.Signal)
	if !ok {
		return cmd.Process.Signal(sig)
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil && pgid > 0 {
		if err := syscall.Kill(-pgid, unixSig); err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
	}

	return cmd.Process.Signal(sig)
}

func killCommandProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil && pgid > 0 {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
	}

	return cmd.Process.Kill()
}
