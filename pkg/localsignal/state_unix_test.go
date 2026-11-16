//go:build !windows

package localsignal

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestSpecialStateFilesFailOpen(t *testing.T) {
	for _, name := range []string{"lock fifo", "state fifo", "state symlink", "lock symlink"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "signals.json")
			target := path
			if name == "lock fifo" || name == "lock symlink" {
				target += ".lock"
			}
			if name == "state symlink" || name == "lock symlink" {
				require.NoError(t, os.Symlink("/dev/zero", target))
			} else {
				require.NoError(t, unix.Mkfifo(target, 0600))
			}
			var output bytes.Buffer
			e := New(&output, nil, nil)
			e.statePath = func() (string, error) { return path, nil }
			e.headless = func() bool { return false }
			attempted := false
			e.notifier = notifyFunc(func(context.Context, string, string, string) error { attempted = true; return nil })
			start := time.Now()
			e.Emit(Denied, Fields{})
			assert.Less(t, time.Since(start), time.Second)
			assert.True(t, attempted, "nonregular state must attempt the notification")
			assert.Contains(t, output.String(), "COMMAND DENIED")
		})
	}
}
