//go:build windows

package localsignal

import (
	"io"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockedTTY struct{ release, closed chan struct{} }

func (w *blockedTTY) Write(p []byte) (int, error) { <-w.release; return len(p), nil }
func (w *blockedTTY) Close() error                { close(w.closed); return nil }

func TestWindowsTTYWriteBound(t *testing.T) {
	for _, stage := range []string{"open", "write"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tty := &blockedTTY{release: make(chan struct{}), closed: make(chan struct{})}
				start := time.Now()
				err := writeTTY(func() (io.WriteCloser, error) {
					if stage == "open" {
						<-tty.release
					}
					return tty, nil
				}, "banner")
				assert.Equal(t, 200*time.Millisecond, time.Since(start))
				close(tty.release)
				<-tty.closed
				require.ErrorIs(t, err, os.ErrDeadlineExceeded)
			})
		})
	}
}
