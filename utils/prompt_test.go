package utils

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptForBoolReadsAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "y", input: "y\n", want: true},
		{name: "yes uppercase", input: "YES\n", want: true},
		{name: "n", input: "n\n", want: false},
		{name: "no with spaces", input: "  no  \n", want: false},
		{name: "invalid then yes", input: "maybe\ny\n", want: true},
		{name: "partial final line yes", input: "yes", want: true},
		{name: "partial final line no", input: "n", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Given
			reader := strings.NewReader(tt.input)

			// When
			got := promptForBool(reader, "Proceed?")

			// Then
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPromptForBoolDeclinesWhenInputEnds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reader io.Reader
	}{
		{name: "empty stdin", reader: strings.NewReader("")},
		{name: "invalid then EOF", reader: strings.NewReader("maybe\n")},
		{name: "invalid partial final line", reader: strings.NewReader("maybe")},
		{name: "read error", reader: iotest.ErrReader(errors.New("stdin closed"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Given
			done := make(chan bool, 1)

			// When
			go func() { done <- promptForBool(tt.reader, "Proceed?") }()

			// Then
			select {
			case got := <-done:
				assert.False(t, got)
			case <-time.After(5 * time.Second):
				t.Fatal("promptForBool kept looping after its input ended")
			}
		})
	}
}

func TestPromptForInputReadsLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "line", input: "my-server\n", want: "my-server"},
		{name: "surrounding spaces", input: "  my-server  \n", want: "my-server"},
		{name: "empty line", input: "\n", want: ""},
		{name: "partial final line", input: "my-server", want: "my-server"},
		{name: "partial final line with spaces", input: "  my-server  ", want: "my-server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Given
			reader := strings.NewReader(tt.input)

			// When
			got, err := promptForInput(reader, "Name: ")

			// Then
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPromptForInputFailsWhenInputEnds(t *testing.T) {
	t.Parallel()

	readErr := errors.New("stdin closed")
	tests := []struct {
		name    string
		reader  io.Reader
		wantErr error
	}{
		{name: "empty stdin", reader: strings.NewReader(""), wantErr: io.EOF},
		{name: "spaces then EOF", reader: strings.NewReader("   "), wantErr: io.EOF},
		{name: "read error", reader: iotest.ErrReader(readErr), wantErr: readErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Given
			reader := tt.reader

			// When
			got, err := promptForInput(reader, "Name: ")

			// Then
			require.ErrorIs(t, err, tt.wantErr)
			assert.Empty(t, got)
		})
	}
}
