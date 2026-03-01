package tunnel

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tunnelruntime "github.com/alpacax/alpacon-cli/pkg/tunnel/runtime"
)

type fakeRunRuntime struct {
	done chan struct{}

	causeMu sync.RWMutex
	cause   error

	checkReadyErr error

	closeCalls int32
	closeOnce  sync.Once
}

func newFakeRunRuntime() *fakeRunRuntime {
	return &fakeRunRuntime{
		done: make(chan struct{}),
	}
}

func (f *fakeRunRuntime) LocalAddress() string {
	return "127.0.0.1:0"
}

func (f *fakeRunRuntime) RemoteAddress() string {
	return "server:0"
}

func (f *fakeRunRuntime) CheckReady() error {
	return f.checkReadyErr
}

func (f *fakeRunRuntime) Done() <-chan struct{} {
	return f.done
}

func (f *fakeRunRuntime) Cause() error {
	f.causeMu.RLock()
	defer f.causeMu.RUnlock()
	return f.cause
}

func (f *fakeRunRuntime) Close(cause error) {
	atomic.AddInt32(&f.closeCalls, 1)
	if cause != nil {
		f.setCause(cause)
	}
	f.closeOnce.Do(func() { close(f.done) })
}

func (f *fakeRunRuntime) triggerRemoteClose(cause error) {
	if cause != nil {
		f.setCause(cause)
	}
	f.closeOnce.Do(func() { close(f.done) })
}

func (f *fakeRunRuntime) setCause(cause error) {
	f.causeMu.Lock()
	defer f.causeMu.Unlock()
	if f.cause == nil {
		f.cause = cause
	}
}

func runHelperCommand(mode string, extraArgs ...string) *exec.Cmd {
	args := []string{"-test.run=TestRunHelperProcess", "--", "run-helper", mode}
	args = append(args, extraArgs...)
	return exec.Command(os.Args[0], args...)
}

func parseRunHelperInvocation(args []string) (mode string, extra []string, ok bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == "run-helper" && i+1 < len(args) {
			return args[i+1], args[i+2:], true
		}
	}
	return "", nil, false
}

func TestExtractRunInvocation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		dashIndex int
		wantSrv   string
		wantCmd   []string
		wantErr   bool
	}{
		{
			name:      "valid invocation",
			args:      []string{"prod-db", "psql", "-c", "select 1"},
			dashIndex: 1,
			wantSrv:   "prod-db",
			wantCmd:   []string{"psql", "-c", "select 1"},
		},
		{
			name:      "missing dash separator",
			args:      []string{"prod-db", "psql"},
			dashIndex: -1,
			wantErr:   true,
		},
		{
			name:      "missing server",
			args:      []string{"psql"},
			dashIndex: 0,
			wantErr:   true,
		},
		{
			name:      "missing local command",
			args:      []string{"prod-db"},
			dashIndex: 1,
			wantErr:   true,
		},
		{
			name:      "too many values before dash",
			args:      []string{"prod-db", "extra", "psql"},
			dashIndex: 2,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSrv, gotCmd, err := extractRunInvocation(tt.args, tt.dashIndex)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotSrv != tt.wantSrv {
				t.Fatalf("server = %q, want %q", gotSrv, tt.wantSrv)
			}
			if !reflect.DeepEqual(gotCmd, tt.wantCmd) {
				t.Fatalf("command = %#v, want %#v", gotCmd, tt.wantCmd)
			}
		})
	}
}

func TestExitCodeFromProcessError(t *testing.T) {
	if code := exitCodeFromProcessError(nil); code != 0 {
		t.Fatalf("nil error exit code = %d, want 0", code)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestExitCodeFromProcessErrorHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected exit error")
	}

	if code := exitCodeFromProcessError(err); code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestExitCodeFromProcessErrorHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	os.Exit(7)
}

func TestMonitorLocalCommandClosesRuntimeOnProcessExit(t *testing.T) {
	runtime := newFakeRunRuntime()
	localCmd := runHelperCommand("exit0")
	if err := localCmd.Start(); err != nil {
		t.Fatalf("failed to start helper command: %v", err)
	}

	exitCode, err := monitorLocalCommand(runtime, localCmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}

	if got := atomic.LoadInt32(&runtime.closeCalls); got != 1 {
		t.Fatalf("runtime.Close called %d times, want 1", got)
	}
	select {
	case <-runtime.Done():
	default:
		t.Fatal("runtime should be closed")
	}
}

func TestMonitorLocalCommandReturnsErrorWhenTunnelCloses(t *testing.T) {
	runtime := newFakeRunRuntime()
	localCmd := runHelperCommand("wait-sigint")
	if err := localCmd.Start(); err != nil {
		t.Fatalf("failed to start helper command: %v", err)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		runtime.triggerRemoteClose(errors.New("session closed by remote"))
	}()

	exitCode, err := monitorLocalCommand(runtime, localCmd)
	if err == nil {
		t.Fatal("expected error when tunnel closes")
	}
	if !strings.Contains(err.Error(), "tunnel connection lost") {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode == 0 {
		t.Fatalf("exitCode = %d, want non-zero", exitCode)
	}
}

func TestExecuteTunnelRunWithInvocationStartFailure(t *testing.T) {
	origStarter := runTunnelStarter
	origFactory := runLocalCommandFactory
	defer func() {
		runTunnelStarter = origStarter
		runLocalCommandFactory = origFactory
	}()

	runTunnelStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return nil, errors.New("start failed")
	}

	exitCode, err := executeTunnelRunWithInvocation("prod-db", []string{"psql"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "start failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
}

func TestExecuteTunnelRunWithInvocationCommandStartFailureClosesRuntime(t *testing.T) {
	origStarter := runTunnelStarter
	origFactory := runLocalCommandFactory
	defer func() {
		runTunnelStarter = origStarter
		runLocalCommandFactory = origFactory
	}()

	runtime := newFakeRunRuntime()
	runTunnelStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return runtime, nil
	}
	runLocalCommandFactory = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/definitely/not/a-real-command")
	}

	exitCode, err := executeTunnelRunWithInvocation("prod-db", []string{"psql"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to start local command") {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if got := atomic.LoadInt32(&runtime.closeCalls); got != 1 {
		t.Fatalf("runtime.Close called %d times, want 1", got)
	}
}

func TestExecuteTunnelRunWithInvocationReadyCheckFailure(t *testing.T) {
	origStarter := runTunnelStarter
	origFactory := runLocalCommandFactory
	defer func() {
		runTunnelStarter = origStarter
		runLocalCommandFactory = origFactory
	}()

	runtime := newFakeRunRuntime()
	runtime.checkReadyErr = errors.New("readiness failed")
	runTunnelStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return runtime, nil
	}

	var commandFactoryCalled int32
	runLocalCommandFactory = func(name string, args ...string) *exec.Cmd {
		atomic.AddInt32(&commandFactoryCalled, 1)
		return runHelperCommand("exit0")
	}

	exitCode, err := executeTunnelRunWithInvocation("prod-db", []string{"psql"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to establish tunnel connection") {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if got := atomic.LoadInt32(&commandFactoryCalled); got != 0 {
		t.Fatalf("local command factory called %d times, want 0", got)
	}
	if got := atomic.LoadInt32(&runtime.closeCalls); got != 1 {
		t.Fatalf("runtime.Close called %d times, want 1", got)
	}
}

func TestExecuteTunnelRunWithInvocationPreservesLocalCommandArgs(t *testing.T) {
	origStarter := runTunnelStarter
	origFactory := runLocalCommandFactory
	defer func() {
		runTunnelStarter = origStarter
		runLocalCommandFactory = origFactory
	}()

	runtime := newFakeRunRuntime()
	runTunnelStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return runtime, nil
	}

	var capturedName string
	var capturedArgs []string
	runLocalCommandFactory = func(name string, args ...string) *exec.Cmd {
		capturedName = name
		capturedArgs = append([]string(nil), args...)
		return runHelperCommand("exit0")
	}

	exitCode, err := executeTunnelRunWithInvocation("prod-k8s", []string{"kubectl", "get", "pods"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}

	if capturedName != "kubectl" {
		t.Fatalf("command name = %q, want kubectl", capturedName)
	}
	wantArgs := []string{"get", "pods"}
	if !reflect.DeepEqual(capturedArgs, wantArgs) {
		t.Fatalf("command args = %#v, want %#v", capturedArgs, wantArgs)
	}
}

func TestExecuteTunnelRunWithInvocationDoesNotInjectTunnelPortEnv(t *testing.T) {
	origStarter := runTunnelStarter
	origFactory := runLocalCommandFactory
	origPort, hadPort := os.LookupEnv("ALPACON_TUNNEL_PORT")
	defer func() {
		runTunnelStarter = origStarter
		runLocalCommandFactory = origFactory
		if hadPort {
			_ = os.Setenv("ALPACON_TUNNEL_PORT", origPort)
		} else {
			_ = os.Unsetenv("ALPACON_TUNNEL_PORT")
		}
	}()
	_ = os.Unsetenv("ALPACON_TUNNEL_PORT")

	envDumpFile := t.TempDir() + "/env.txt"

	runtime := newFakeRunRuntime()
	runTunnelStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return runtime, nil
	}
	runLocalCommandFactory = func(name string, args ...string) *exec.Cmd {
		return runHelperCommand("print-env", envDumpFile)
	}

	exitCode, err := executeTunnelRunWithInvocation("prod-db", []string{"psql"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}

	data, err := os.ReadFile(envDumpFile)
	if err != nil {
		t.Fatalf("failed to read env dump: %v", err)
	}
	envDump := string(data)
	if strings.Contains(envDump, "ALPACON_TUNNEL_PORT=") {
		t.Fatalf("unexpected ALPACON_TUNNEL_PORT in env dump:\n%s", envDump)
	}
}

func TestRunHelperProcess(t *testing.T) {
	mode, extra, ok := parseRunHelperInvocation(os.Args)
	if !ok {
		return
	}

	switch mode {
	case "exit0":
		os.Exit(0)
	case "wait-sigint":
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sigChan:
			os.Exit(7)
		case <-time.After(10 * time.Second):
			os.Exit(8)
		}
	case "print-env":
		if len(extra) < 1 {
			os.Exit(2)
		}
		err := os.WriteFile(extra[0], []byte(strings.Join(os.Environ(), "\n")), 0o600)
		if err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
