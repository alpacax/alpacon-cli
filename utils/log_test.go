package utils

import (
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStderr returns everything fn writes to stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	_, stderr := testutil.CaptureOutput(t, fn)
	return stderr
}

// pinColor fixes the color switch for one test, whatever stderr happens to be.
func pinColor(t *testing.T, enabled bool) {
	t.Helper()
	original := colorEnabled
	colorEnabled = enabled
	t.Cleanup(func() { colorEnabled = original })
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

// The server's error detail reaches these helpers through %s.
func TestCliHelpers_SanitizeServerControlledText(t *testing.T) {
	t.Setenv(DebugEnvVar, "1")
	// Red("Error") and its siblings carry an escape of their own, so the ANSI
	// case would fail whenever the test binary's stderr is a terminal.
	pinColor(t, false)
	emitters := map[string]func(string, ...any){
		"CliError":   CliError,
		"CliWarning": CliWarning,
		"CliInfo":    CliInfo,
		"CliSuccess": CliSuccess,
		"CliDebug":   CliDebug,
	}
	tests := []struct {
		name    string
		detail  string
		notWant string
	}{
		{name: "ANSI escape", detail: "denied\x1b[2Kapproved", notWant: "\x1b"},
		{name: "bidi override", detail: "denied\u202eapproved", notWant: "\u202e"},
		{name: "carriage return", detail: "denied\rapproved", notWant: "\r"},
		{name: "8-bit CSI", detail: "denied\u009b2Kapproved", notWant: "\u009b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, emit := range emitters {
				stderr := captureStderr(t, func() { emit("%s", tt.detail) })
				assert.NotContains(t, stderr, tt.notWant, name)
				assert.Contains(t, stderr, "denied", name)
				assert.Contains(t, stderr, "approved", name)
			}
		})
	}
}

// Sanitizing the rendered message cannot tell a caller's newline from the
// server's, so the detail keeps the power to open a line of its own.
func TestCliHelpers_KeepServerNewlines(t *testing.T) {
	stderr := captureStderr(t, func() {
		CliError("%s", "denied\nSuccess: approved")
	})

	assert.Contains(t, stderr, "denied\nSuccess: approved\n")
}

func TestCliHelpers_KeepCallerNewlines(t *testing.T) {
	stderr := captureStderr(t, func() {
		CliWarning("first line\nsecond line: %s", "value")
	})

	assert.Contains(t, stderr, "first line\nsecond line: value\n")
}

// The choke point strips its arguments too, so a pre-colored value comes out
// plain. cmd/server relies on the reverse order—sanitize the key, then color
// it—to keep the "copy this" highlight on a registration key.
func TestCliMessage_StripsColorFromArguments(t *testing.T) {
	pinColor(t, true)

	assert.Equal(t, "Save this key: secret", cliMessage("Save this key: %s", Green("secret")))
}
