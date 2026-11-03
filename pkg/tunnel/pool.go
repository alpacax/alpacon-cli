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

// CopyBuffered copies src to dst through a pooled buffer, hiding io.WriterTo and io.ReaderFrom so io.CopyBuffer cannot bypass it.
// A failed copy leaves the buffer to the GC: a writer such as a smux stream can return an error while still reading it.
func CopyBuffered(dst io.Writer, src io.Reader) (int64, error) {
	buf := copyBuffers.Get().(*[]byte)
	n, err := io.CopyBuffer(struct{ io.Writer }{dst}, struct{ io.Reader }{src}, *buf)
	if err == nil {
		copyBuffers.Put(buf)
	}
	return n, err
}
