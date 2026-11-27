package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureOutput(t *testing.T, fn func(stdout *os.File)) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	fn(w)
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()

	return buf.String()
}

func TestWriteToPager_NonTerminal(t *testing.T) {
	t.Parallel()
	want := "hello\nworld\n"

	got := captureOutput(t, func(stdout *os.File) {
		w, cleanup := writeToPager(false, nil, stdout)
		_, _ = fmt.Fprint(w, want)
		require.NoError(t, cleanup())
	})

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteToPager_TerminalShortOutput(t *testing.T) {
	t.Parallel()
	want := "line1\nline2\nline3\n"

	got := captureOutput(t, func(stdout *os.File) {
		w, cleanup := writeToPager(true, func() (int, error) { return 100, nil }, stdout)
		_, _ = fmt.Fprint(w, want)
		require.NoError(t, cleanup())
	})

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteToPager_TerminalLongOutput(t *testing.T) {
	t.Setenv("PAGER", "cat")

	var sb strings.Builder
	for i := range 20 {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	want := sb.String()

	got := captureOutput(t, func(stdout *os.File) {
		w, cleanup := writeToPager(true, func() (int, error) { return 5, nil }, stdout)
		_, _ = fmt.Fprint(w, want)
		require.NoError(t, cleanup())
	})

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Serial: it sets PAGER and reads the package-level record of the last flush.
func TestWriteToPager_RecordsWhetherAPagerRan(t *testing.T) {
	long := strings.Repeat("line\n", 20)
	tests := []struct {
		name   string
		pager  string
		height int
		want   bool
	}{
		{name: "a pager that runs", pager: "cat", height: 5, want: true},
		// The shell expands ${IFS} inside the script passed as a single -c argument.
		{name: "a pager that exits nonzero without reading", pager: "sh -c exit${IFS}3", height: 5, want: false},
		{name: "a pager that exits nonzero after printing", pager: "sh -c cat;exit${IFS}3", height: 5, want: true},
		{name: "a pager that cannot start falls back to stdout", pager: "alpacon-no-such-pager", height: 5, want: false},
		{name: "output that fits is not paged", pager: "cat", height: 100, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PAGER", tt.pager)
			lastOutputPaged.Store(!tt.want)

			got := captureOutput(t, func(stdout *os.File) {
				w, cleanup := writeToPager(true, func() (int, error) { return tt.height, nil }, stdout)
				_, _ = fmt.Fprint(w, long)
				require.NoError(t, cleanup())
			})

			if got != long {
				t.Errorf("output = %q, want it written once", got)
			}
			if LastOutputPaged() != tt.want {
				t.Errorf("LastOutputPaged() = %v, want %v", LastOutputPaged(), tt.want)
			}
		})
	}
}

func TestWriteToPagerReportsFlushFailure(t *testing.T) {
	t.Parallel()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	require.NoError(t, stdout.Close())
	writer, flush := writeToPager(false, nil, stdout)
	_, err = io.WriteString(writer, "approval detail\n")
	require.NoError(t, err)

	assert.ErrorIs(t, flush(), os.ErrClosed)
}
