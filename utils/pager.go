package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"

	"golang.org/x/term"
)

// WriteToPager returns a writer that buffers output and pipes it through a pager
// only when the output exceeds the terminal height.
// The caller must call the returned cleanup function when done writing.
//
// Behavior:
//   - If stdout is not a terminal (e.g. piped), prints directly to stdout.
//   - If the output fits within the terminal height, prints directly to stdout.
//   - Otherwise, pipes through the PAGER environment variable or "less -RSX".
//   - Falls back to direct stdout if the pager cannot start.
//   - Once the pager starts, it is the sole output path. WriteToPager discards
//     errors from an unsuccessful pager exit. IOStreams.PagerWithError reports them.
func WriteToPager() (io.Writer, func()) {
	writer, flush := writeToStdoutPager()
	return writer, func() { _ = flush() }
}

func writeToStdoutPager() (io.Writer, func() error) {
	return writeToPager(
		term.IsTerminal(int(os.Stdout.Fd())),
		func() (int, error) {
			_, h, err := term.GetSize(int(os.Stdout.Fd()))
			return h, err
		},
		os.Stdout,
	)
}

// lastOutputPaged records whether the latest flush ran a pager that exited successfully.
var lastOutputPaged atomic.Bool

// LastOutputPaged reports whether the last flush ran a pager that exited successfully, so
// a caller can repeat on stderr what the pager scrolled away.
func LastOutputPaged() bool {
	return lastOutputPaged.Load()
}

func writeToPager(isTerminal bool, getHeight func() (int, error), stdout *os.File) (io.Writer, func() error) {
	var buf bytes.Buffer

	flush := func() error {
		lastOutputPaged.Store(false)
		output := buf.Bytes()
		writeDirect := func() error {
			_, err := stdout.Write(output)
			return err
		}

		if !isTerminal {
			return writeDirect()
		}

		height, err := getHeight()
		lineCount := bytes.Count(output, []byte("\n"))
		if err != nil || lineCount <= height {
			return writeDirect()
		}

		pager := os.Getenv("PAGER")
		args := strings.Fields(pager)
		var cmd *exec.Cmd
		if len(args) > 0 {
			cmd = exec.Command(args[0], args[1:]...)
		} else {
			lessPath, err := exec.LookPath("less")
			if err != nil {
				return writeDirect()
			}
			cmd = exec.Command(lessPath, "-RSX")
		}

		cmd.Stdin = bytes.NewReader(output)
		cmd.Stdout = stdout
		cmd.Stderr = StderrForChild()

		if err := cmd.Start(); err != nil {
			return writeDirect()
		}
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("pager %s exited: %w", cmd.Args[0], err)
		}
		lastOutputPaged.Store(true)
		return nil
	}

	return &buf, flush
}
