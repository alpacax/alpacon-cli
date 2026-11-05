package tunnel

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

type readFromWriter struct {
	writes []int
}

func (w *readFromWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, cap(p))
	return len(p), nil
}

func (w *readFromWriter) ReadFrom(io.Reader) (int64, error) {
	return 0, errors.New("ReadFrom bypassed the pooled buffer")
}

func TestCopyBufferedWritesThroughThePooledBufferWhateverTheDestinationImplements(t *testing.T) {
	t.Parallel()
	dst := &readFromWriter{}

	n, err := CopyBuffered(dst, strings.NewReader("data"))

	require.NoError(t, err)
	assert.Equal(t, int64(4), n)
	assert.Equal(t, []int{32 * 1024}, dst.writes)
}

func TestCopyBufferedNeverHandsOutABufferAWriteFailedOn(t *testing.T) {
	t.Parallel()
	const failures = 16 // the race detector drops a quarter of pool puts, so one failure could miss a regression
	failedOn := make(map[*byte]bool)
	for range failures {
		_, err := CopyBuffered(writerFunc(func(p []byte) (int, error) {
			failedOn[&p[0]] = true
			return 0, errors.New("stream closed")
		}), strings.NewReader("data"))
		require.Error(t, err)
	}
	require.Len(t, failedOn, failures)

	for range 64 {
		var used *byte
		_, err := CopyBuffered(writerFunc(func(p []byte) (int, error) {
			used = &p[0]
			return len(p), nil
		}), strings.NewReader("data"))
		require.NoError(t, err)
		require.NotNil(t, used)
		require.False(t, failedOn[used], "smux may still read a buffer after a failed write")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// Serial: parallel copies draw from the same pool.
func TestCopyBufferedReusesTheBufferAfterAReadError(t *testing.T) {
	const copies = 16 // the race detector drops a quarter of pool puts
	readFailedOn := make(map[*byte]bool)
	for range copies {
		var used *byte
		_, err := CopyBuffered(writerFunc(func(p []byte) (int, error) {
			used = &p[0]
			return len(p), nil
		}), io.MultiReader(strings.NewReader("data"), failingReader{}))
		require.Error(t, err)
		require.NotNil(t, used)
		readFailedOn[used] = true
	}

	assert.Less(t, len(readFailedOn), copies, "a read error leaves no write holding the buffer")
}
