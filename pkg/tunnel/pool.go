package tunnel

import (
	"io"
	"sync"
)

var copyBuffer = &bufferPool{
	size: 32 * 1024,
	pool: sync.Pool{
		New: func() any {
			b := make([]byte, 32*1024)
			return &b
		},
	},
}

type bufferPool struct {
	pool sync.Pool
	size int
}

func (p *bufferPool) get() []byte {
	return *p.pool.Get().(*[]byte)
}

func (p *bufferPool) put(buf []byte) {
	if cap(buf) == p.size {
		buf = buf[:p.size]
		p.pool.Put(&buf)
	}
}

// CopyBuffered performs io.CopyBuffer using pooled buffers.
func CopyBuffered(dst io.Writer, src io.Reader) (int64, error) {
	buf := copyBuffer.get()
	defer copyBuffer.put(buf)
	return io.CopyBuffer(dst, src, buf)
}
