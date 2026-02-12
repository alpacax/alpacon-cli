package utils

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"

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
//   - Falls back to direct stdout if the pager command is not available.
func WriteToPager() (io.Writer, func()) {
	return writeToPager(
		term.IsTerminal(int(os.Stdout.Fd())),
		func() (int, error) {
			_, h, err := term.GetSize(int(os.Stdout.Fd()))
			return h, err
		},
		os.Stdout,
	)
}

func writeToPager(isTerminal bool, getHeight func() (int, error), stdout *os.File) (io.Writer, func()) {
	var buf bytes.Buffer

	cleanup := func() {
		output := buf.Bytes()

		if !isTerminal {
			stdout.Write(output)
			return
		}

		height, err := getHeight()
		lineCount := bytes.Count(output, []byte("\n"))
		if err != nil || lineCount <= height {
			stdout.Write(output)
			return
		}

		pager := os.Getenv("PAGER")
		args := strings.Fields(pager)
		var cmd *exec.Cmd
		if len(args) > 0 {
			cmd = exec.Command(args[0], args[1:]...)
		} else {
			lessPath, err := exec.LookPath("less")
			if err != nil {
				stdout.Write(output)
				return
			}
			cmd = exec.Command(lessPath, "-RSX")
		}

		cmd.Stdin = bytes.NewReader(output)
		cmd.Stdout = stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			stdout.Write(output)
		}
	}

	return &buf, cleanup
}
