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

// signalingEditFile sends sig to its own process mid-write and marks the moment it is closed.
type signalingEditFile struct {
	*os.File
	sig    syscall.Signal
	marker string
}

func (f *signalingEditFile) Write([]byte) (int, error) {
	_ = syscall.Kill(os.Getpid(), f.sig)
	select {} // the write never finishes; only the signal path can end the process
}

func (f *signalingEditFile) Close() error {
	_ = os.WriteFile(f.marker, nil, 0o600)
	return f.File.Close()
}

// runSignalHelper runs the helper process in mode and asserts it died by sig.
func runSignalHelper(t *testing.T, mode, path string, sig syscall.Signal) {
	t.Helper()
	help := osexec.Command(os.Args[0], "-test.run=^TestCleanupOnSignalHelperProcess$")
	help.Env = append(os.Environ(),
		"GO_WANT_SIGNAL_HELPER="+mode, "GO_SIGNAL_HELPER_PATH="+path, "GO_SIGNAL_HELPER_SIG="+sig.String())

	err := help.Run()

	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok, "unexpected wait status type %T", exitErr.Sys())
	assert.True(t, status.Signaled(), "child must die by the signal, got %v", status)
	assert.Equal(t, sig, status.Signal())
}

// Serial: the subject runs in a helper process.
func TestCleanupOnSignalRunsCleanupAndDiesBySignal(t *testing.T) {
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

			runSignalHelper(t, "cleanup", path, tt.sig)

			assert.NoFileExists(t, path)
		})
	}
}

// Serial: the subject runs in a helper process. Windows refuses to remove an open file,
// so the signal path must close the file it is still writing before removing it.
func TestEditTempFileClosesTheFileBeforeRemovingItOnSignal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")

	runSignalHelper(t, "edit", path, syscall.SIGTERM)

	assert.NoFileExists(t, path)
	assert.FileExists(t, path+".closed")
}

func TestCleanupOnSignalHelperProcess(t *testing.T) {
	mode := os.Getenv("GO_WANT_SIGNAL_HELPER")
	if mode == "" {
		return
	}
	path := os.Getenv("GO_SIGNAL_HELPER_PATH")
	sig := map[string]syscall.Signal{
		syscall.SIGHUP.String():  syscall.SIGHUP,
		syscall.SIGTERM.String(): syscall.SIGTERM,
		syscall.SIGINT.String():  syscall.SIGINT,
	}[os.Getenv("GO_SIGNAL_HELPER_SIG")]

	switch mode {
	case "cleanup":
		cleanupOnSignal(func() { _ = os.Remove(path) })
		_ = syscall.Kill(os.Getpid(), sig)
	case "edit":
		file, err := os.Create(path)
		if err != nil {
			os.Exit(3)
		}
		f := &signalingEditFile{File: file, sig: sig, marker: path + ".closed"}
		_, _ = editTempFile(f, []byte(`{"name":"my-webhook"}`), func(string) error { return nil })
	}
	select {} // reraise ends the process; reaching past it would hang the parent's timeout
}

// Serial: cleanupOnSignal changes the process-wide signal disposition while it is armed.
func TestCleanupOnSignalSkipsCleanupAfterStop(t *testing.T) {
	called := false

	cleanupOnSignal(func() { called = true })()

	assert.False(t, called, "cleanup must be left to the caller's defer once stopped")
}
