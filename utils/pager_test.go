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
