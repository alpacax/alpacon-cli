package tunnel

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

type readerOnly struct{ r *strings.Reader }

func (o readerOnly) Read(p []byte) (int, error) { return o.r.Read(p) }

func TestCopyBufferedNeverHandsOutABufferAWriteFailedOn(t *testing.T) {
	t.Parallel()
	var failedOn *byte
	_, err := CopyBuffered(writerFunc(func(p []byte) (int, error) {
		failedOn = &p[0]
		return 0, errors.New("stream closed")
	}), readerOnly{strings.NewReader("data")})
	require.Error(t, err)
	require.NotNil(t, failedOn)

	for range 64 {
		var used *byte
		_, err := CopyBuffered(writerFunc(func(p []byte) (int, error) {
			used = &p[0]
			return len(p), nil
		}), readerOnly{strings.NewReader("data")})
		require.NoError(t, err)
		require.NotSame(t, failedOn, used, "smux may still read a buffer after a failed write")
	}
}
