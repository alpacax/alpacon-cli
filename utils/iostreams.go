package utils

import (
	"bytes"
	"io"
	"os"

	"golang.org/x/term"
)

// IOStreams is where a command writes: data to Out, messages to ErrOut, in the --output format
// it holds. Commands take one through their constructor so a test can hand in buffers.
type IOStreams struct {
	Out    io.Writer
	ErrOut io.Writer
	format *string
	// ColorEnabled gates ANSI codes; it follows ErrOut, where colored messages go, so a pipe
	// or a redirected log carries none, and NO_COLOR turns it off.
	ColorEnabled bool
}

// Stdio is the process's own streams, which the package-level print helpers write through.
var Stdio = SystemIOStreams()

// SystemIOStreams writes to whatever os.Stdout and os.Stderr are at the time of each write, under
// the format the --output flag set.
func SystemIOStreams() *IOStreams {
	return &IOStreams{
		Out:          stdoutWriter{},
		ErrOut:       TrackedStderr(),
		format:       &OutputFormat,
		ColorEnabled: colorAllowed(os.Getenv("NO_COLOR"), term.IsTerminal(int(os.Stderr.Fd()))),
	}
}

// colorAllowed follows no-color.org: any non-empty NO_COLOR turns color off.
func colorAllowed(noColor string, stderrIsTerminal bool) bool {
	return noColor == "" && stderrIsTerminal
}

// TestIOStreams returns streams writing to the two buffers, in table format with no color.
func TestIOStreams() (ios *IOStreams, out, errOut *bytes.Buffer) {
	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	format := OutputFormatTable
	return &IOStreams{Out: out, ErrOut: errOut, format: &format}, out, errOut
}

// OutputFormat is the --output value these streams print in.
func (s *IOStreams) OutputFormat() string { return *s.format }

// IsJSON reports whether these streams print --output json.
func (s *IOStreams) IsJSON() bool { return *s.format == OutputFormatJSON }

// SetOutputFormat applies an --output value, refusing anything but table or json as a usage error.
func (s *IOStreams) SetOutputFormat(value string) error {
	if value != OutputFormatTable && value != OutputFormatJSON {
		return invalidOutputFormat(value)
	}
	*s.format = value
	return nil
}

// stdoutWriter writes to os.Stdout as the process has it at each write, so a test that swaps
// os.Stdout still sees what the package-level helpers print.
type stdoutWriter struct{}

func (stdoutWriter) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

// Pager returns where a table goes: through the pager for the process's stdout, and straight
// to Out for any other writer, which is never a terminal.
func (s *IOStreams) Pager() (io.Writer, func()) {
	if _, ok := s.Out.(stdoutWriter); ok {
		return WriteToPager()
	}
	return s.Out, func() {}
}

// isProcessStderr reports whether w is the process's stderr, where a spinner or a
// partial line may have left the cursor mid-line.
func isProcessStderr(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return f == os.Stderr
	}
	_, ok := w.(stderrTracker)
	return ok
}
