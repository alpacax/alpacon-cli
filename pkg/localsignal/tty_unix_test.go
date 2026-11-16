//go:build unix

package localsignal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestTTYForegroundCheck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		foreground bool
		err        error
	}{
		{name: "foreground", foreground: true},
		{name: "background"},
		{name: "ioctl failure", err: unix.ENOTTY},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name := filepath.Join(t.TempDir(), "tty")
			require.NoError(t, os.WriteFile(name, nil, 0600))
			tty, err := openTTYFile(name, func(opened int) (bool, error) {
				flags, flagErr := unix.FcntlInt(uintptr(opened), unix.F_GETFL, 0)
				require.NoError(t, flagErr)
				assert.Equal(t, unix.O_WRONLY, flags&unix.O_ACCMODE)
				assert.NotZero(t, flags&unix.O_NONBLOCK)
				return tc.foreground, tc.err
			})
			if tc.foreground {
				require.NoError(t, err)
				_, err = io.WriteString(tty, "banner")
				assert.NoError(t, err)
				require.NoError(t, tty.Close())
				data, readErr := os.ReadFile(name)
				require.NoError(t, readErr)
				assert.Equal(t, "banner", string(data))
			} else {
				require.Error(t, err)
				assert.Nil(t, tty)
				data, readErr := os.ReadFile(name)
				require.NoError(t, readErr)
				assert.Empty(t, data)
			}
		})
	}
}

func TestTTYBackgroundOrErrorStillNotifies(t *testing.T) {
	t.Parallel()
	for _, checkErr := range []error{nil, unix.ENOTTY} {
		name := "background"
		if checkErr != nil {
			name = "ioctl failure"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "tty")
			require.NoError(t, os.WriteFile(path, nil, 0600))
			var output bytes.Buffer
			e := New(&output, nil, nil)
			e.JSON = true
			e.statePath = func() (string, error) { return "", errors.New("no state") }
			e.openTTY = func() (io.WriteCloser, error) {
				return openTTYFile(path, func(int) (bool, error) { return false, checkErr })
			}
			e.headless = func() bool { return false }
			notified := make(chan struct{}, 1)
			e.notifier = notifyFunc(func(context.Context, string, string, string) error { notified <- struct{}{}; return nil })
			e.Emit(Paused, Fields{})
			<-notified
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Empty(t, data)
			assert.Empty(t, output.String())
		})
	}
}

func TestTTYWriteEAGAINStillNotifies(t *testing.T) {
	t.Parallel()
	fds := make([]int, 2)
	require.NoError(t, unix.Pipe(fds))
	defer func() { _ = unix.Close(fds[0]) }()
	require.NoError(t, unix.SetNonblock(fds[1], true))
	tty := &unixTTY{fd: fds[1], isForeground: func(int) (bool, error) { return true, nil }}
	emitted := false
	defer func() {
		if !emitted {
			_ = tty.Close()
		}
	}()
	for {
		_, err := tty.Write(make([]byte, 4096))
		if err != nil {
			require.ErrorIs(t, err, unix.EAGAIN)
			break
		}
	}
	_, err := tty.Write([]byte("banner"))
	require.ErrorIs(t, err, unix.EAGAIN)
	var output bytes.Buffer
	e := New(&output, nil, nil)
	e.JSON = true
	e.statePath = func() (string, error) { return "", errors.New("no state") }
	e.openTTY = func() (io.WriteCloser, error) { return tty, nil }
	e.headless = func() bool { return false }
	notified := make(chan struct{}, 1)
	e.notifier = notifyFunc(func(context.Context, string, string, string) error { notified <- struct{}{}; return nil })
	e.Emit(Paused, Fields{})
	emitted = true
	<-notified
	assert.Empty(t, output.String())
}
