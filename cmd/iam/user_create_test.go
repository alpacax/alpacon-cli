package iam

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintGeneratedPassword(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		password   string
		wantStdout string
		wantStderr string
	}{
		{"password present", "Gen3rated-Pw!x", "Gen3rated-Pw!x\n", "info: This is the generated password"},
		{"password empty", "", "", "warn: The server returned no password"},
		{"password with an ANSI escape is withheld", "\x1b[31mpw\x1b[0m", "", "warn: The generated password contains characters that cannot be shown safely"},
		{"password with a format character is withheld", "pw\u202eab", "", "warn: The generated password contains characters that cannot be shown safely"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			record := func(level string) func(string, ...any) {
				return func(msg string, args ...any) { _, _ = fmt.Fprintf(&stderr, level+": "+msg, args...) }
			}

			err := printGeneratedPassword(&stdout, record("info"), record("warn"), tt.password)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStdout, stdout.String())
			assert.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

func TestPrintGeneratedPassword_ReportsWriteFailure(t *testing.T) {
	t.Parallel()
	ignore := func(string, ...any) {}

	err := printGeneratedPassword(failingWriter{}, ignore, ignore, "Gen3rated-Pw!x")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no space left on device")
}
