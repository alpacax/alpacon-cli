package tunnel

import (
	"io"
	"sync"
)

var copyBuffers = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

// CopyBuffered copies src to dst through a pooled buffer, hiding io.WriterTo and io.ReaderFrom so the copy uses it.
// The buffer goes back only after a clean copy: a failed smux write can return while smux still reads the buffer.
func CopyBuffered(dst io.Writer, src io.Reader) (int64, error) {
	buf := copyBuffers.Get().(*[]byte)
	n, err := io.CopyBuffer(struct{ io.Writer }{dst}, struct{ io.Reader }{src}, *buf)
	if err == nil {
		copyBuffers.Put(buf)
	}
	return n, err
}
