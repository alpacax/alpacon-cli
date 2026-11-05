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
// A failed write leaves the buffer to the GC: a writer such as a smux stream can return an error while still reading it.
func CopyBuffered(dst io.Writer, src io.Reader) (int64, error) {
	buf := copyBuffers.Get().(*[]byte)
	w := &failureRecordingWriter{w: dst}
	n, err := io.CopyBuffer(w, struct{ io.Reader }{src}, *buf)
	if !w.failed {
		copyBuffers.Put(buf)
	}
	return n, err
}

type failureRecordingWriter struct {
	w      io.Writer
	failed bool
}

func (f *failureRecordingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil || n < len(p) {
		f.failed = true
	}
	return n, err
}
