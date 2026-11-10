//go:build unix

package utils

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: the child process is the subject, and it runs in a helper process.
func TestRemoveOnSignalRemovesTheFileAndDiesBySignal(t *testing.T) {
	tests := []struct {
		name string
		sig  syscall.Signal
	}{
		{"terminal hangup", syscall.SIGHUP},
		{"termination request", syscall.SIGTERM},
		{"interrupt", syscall.SIGINT},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record.json")
			require.NoError(t, os.WriteFile(path, []byte(`{"name":"my-webhook"}`), 0o600))
			help := osexec.Command(os.Args[0], "-test.run=^TestRemoveOnSignalHelperProcess$")
			help.Env = append(os.Environ(), "GO_WANT_SIGNAL_HELPER="+path, "GO_SIGNAL_HELPER_SIG="+tt.sig.String())

			err := help.Run()

			var exitErr *osexec.ExitError
			require.ErrorAs(t, err, &exitErr)
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			require.True(t, ok, "unexpected wait status type %T", exitErr.Sys())
			assert.True(t, status.Signaled(), "child must die by the signal, got %v", status)
			assert.Equal(t, tt.sig, status.Signal())
			assert.NoFileExists(t, path)
		})
	}
}

func TestRemoveOnSignalHelperProcess(t *testing.T) {
	path := os.Getenv("GO_WANT_SIGNAL_HELPER")
	if path == "" {
		return
	}
	sig := map[string]syscall.Signal{
		syscall.SIGHUP.String():  syscall.SIGHUP,
		syscall.SIGTERM.String(): syscall.SIGTERM,
		syscall.SIGINT.String():  syscall.SIGINT,
	}[os.Getenv("GO_SIGNAL_HELPER_SIG")]
	removeOnSignal(path)
	_ = syscall.Kill(os.Getpid(), sig)
	select {} // reraise ends the process; reaching past it would hang the parent's timeout
}

// Serial: removeOnSignal changes the process-wide signal disposition while it is armed.
func TestRemoveOnSignalKeepsTheFileAfterStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"name":"my-webhook"}`), 0o600))

	removeOnSignal(path)()

	assert.FileExists(t, path, "stop must leave the file to the caller's defer")
}
