package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
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
		cleanup()
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
		cleanup()
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
		cleanup()
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
		{name: "a pager that fails falls back to stdout", pager: "alpacon-no-such-pager", height: 5, want: false},
		{name: "output that fits is not paged", pager: "cat", height: 100, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PAGER", tt.pager)

			got := captureOutput(t, func(stdout *os.File) {
				w, cleanup := writeToPager(true, func() (int, error) { return tt.height, nil }, stdout)
				_, _ = fmt.Fprint(w, long)
				cleanup()
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
