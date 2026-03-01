package tunnel

import (
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tunnelruntime "github.com/alpacax/alpacon-cli/pkg/tunnel/runtime"
)

type fakeTunnelCommandRuntime struct {
	done chan struct{}

	causeMu sync.RWMutex
	cause   error

	checkReadyErr error

	closeCalls int32
	closeOnce  sync.Once
}

func newFakeTunnelCommandRuntime() *fakeTunnelCommandRuntime {
	return &fakeTunnelCommandRuntime{
		done: make(chan struct{}),
	}
}

func (f *fakeTunnelCommandRuntime) LocalAddress() string {
	return "127.0.0.1:15432"
}

func (f *fakeTunnelCommandRuntime) RemoteAddress() string {
	return "prod-db:5432"
}

func (f *fakeTunnelCommandRuntime) CheckReady() error {
	return f.checkReadyErr
}

func (f *fakeTunnelCommandRuntime) Done() <-chan struct{} {
	return f.done
}

func (f *fakeTunnelCommandRuntime) Cause() error {
	f.causeMu.RLock()
	defer f.causeMu.RUnlock()
	return f.cause
}

func (f *fakeTunnelCommandRuntime) Close(cause error) {
	atomic.AddInt32(&f.closeCalls, 1)
	if cause != nil {
		f.causeMu.Lock()
		if f.cause == nil {
			f.cause = cause
		}
		f.causeMu.Unlock()
	}
	f.closeOnce.Do(func() { close(f.done) })
}

func (f *fakeTunnelCommandRuntime) closeFromRemote(cause error) {
	if cause != nil {
		f.causeMu.Lock()
		if f.cause == nil {
			f.cause = cause
		}
		f.causeMu.Unlock()
	}
	f.closeOnce.Do(func() { close(f.done) })
}

func TestExecuteTunnelStartFailure(t *testing.T) {
	origStarter := tunnelCommandStarter
	defer func() { tunnelCommandStarter = origStarter }()

	tunnelCommandStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return nil, errors.New("start failed")
	}

	err := executeTunnel("prod-db", make(chan os.Signal, 1))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "start failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteTunnelRemoteCloseReturnsCause(t *testing.T) {
	origStarter := tunnelCommandStarter
	defer func() { tunnelCommandStarter = origStarter }()

	runtime := newFakeTunnelCommandRuntime()
	runtime.closeFromRemote(errors.New("session closed by remote"))
	tunnelCommandStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return runtime, nil
	}

	err := executeTunnel("prod-db", make(chan os.Signal, 1))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tunnel connection lost") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteTunnelInterruptClosesRuntime(t *testing.T) {
	origStarter := tunnelCommandStarter
	defer func() { tunnelCommandStarter = origStarter }()

	runtime := newFakeTunnelCommandRuntime()
	tunnelCommandStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return runtime, nil
	}

	sigChan := make(chan os.Signal, 1)
	sigChan <- os.Interrupt

	err := executeTunnel("prod-db", sigChan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := atomic.LoadInt32(&runtime.closeCalls); got == 0 {
		t.Fatal("expected runtime.Close to be called")
	}
}

func TestExecuteTunnelReadyCheckFailure(t *testing.T) {
	origStarter := tunnelCommandStarter
	defer func() { tunnelCommandStarter = origStarter }()

	runtime := newFakeTunnelCommandRuntime()
	runtime.checkReadyErr = errors.New("readiness failed")
	tunnelCommandStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return runtime, nil
	}

	err := executeTunnel("prod-db", make(chan os.Signal, 1))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to establish tunnel connection") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&runtime.closeCalls); got != 1 {
		t.Fatalf("runtime.Close called %d times, want 1", got)
	}
}

func TestExecuteTunnelRemoteCloseWithoutCauseReturnsNil(t *testing.T) {
	origStarter := tunnelCommandStarter
	defer func() { tunnelCommandStarter = origStarter }()

	runtime := newFakeTunnelCommandRuntime()
	runtime.closeFromRemote(nil)
	tunnelCommandStarter = func(opts tunnelruntime.StartOptions) (tunnelCommandRuntime, error) {
		return runtime, nil
	}

	err := executeTunnel("prod-db", make(chan os.Signal, 1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
