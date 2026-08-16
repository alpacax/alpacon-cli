package utils

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStderr returns everything fn writes to stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = original }()

	captured := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		captured <- string(data)
	}()

	fn()
	require.NoError(t, writer.Close())
	return <-captured
}

func TestDebugEnabled(t *testing.T) {
	tests := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{name: "unset", set: false},
		{name: "empty", set: true, value: ""},
		{name: "zero", set: true, value: "0"},
		{name: "false", set: true, value: "false"},
		{name: "FALSE", set: true, value: "FALSE"},
		{name: "one", set: true, value: "1", want: true},
		{name: "true", set: true, value: "true", want: true},
		{name: "anything else", set: true, value: "verbose", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(DebugEnvVar, tt.value)
			} else {
				require.NoError(t, os.Unsetenv(DebugEnvVar))
			}
			assert.Equal(t, tt.want, DebugEnabled())
		})
	}
}

func TestCliDebug(t *testing.T) {
	t.Run("writes to stderr when the switch is on", func(t *testing.T) {
		t.Setenv(DebugEnvVar, "1")
		stderr := captureStderr(t, func() { CliDebug("fell back to %s", "the fingerprint") })
		assert.Contains(t, stderr, "Debug: fell back to the fingerprint\n")
	})

	t.Run("stays silent when the switch is off", func(t *testing.T) {
		t.Setenv(DebugEnvVar, "0")
		stderr := captureStderr(t, func() { CliDebug("fell back to %s", "the fingerprint") })
		assert.Empty(t, stderr)
	})
}
