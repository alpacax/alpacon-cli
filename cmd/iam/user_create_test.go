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
		wantErr    string
	}{
		{"password present", "Gen3rated-Pw!x", "Gen3rated-Pw!x\n", ""},
		{"password empty", "", "", "the server returned no password"},
		{"password with an ANSI escape is withheld", "\x1b[31mpw\x1b[0m", "", "the generated password contains characters that cannot be shown safely"},
		{"password with a format character is withheld", "pw\u202eab", "", "the generated password contains characters that cannot be shown safely"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			info := func(msg string, args ...any) { _, _ = fmt.Fprintf(&stderr, msg, args...) }

			err := printGeneratedPassword(&stdout, info, tt.password)

			assert.Equal(t, tt.wantStdout, stdout.String())
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Contains(t, stderr.String(), "This is the generated password")
				return
			}
			require.EqualError(t, err, tt.wantErr)
			assert.Empty(t, stderr.String())
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

func TestPrintGeneratedPassword_ReportsWriteFailure(t *testing.T) {
	t.Parallel()
	ignore := func(string, ...any) {}

	err := printGeneratedPassword(failingWriter{}, ignore, "Gen3rated-Pw!x")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no space left on device")
}
