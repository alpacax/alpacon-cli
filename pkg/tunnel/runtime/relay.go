package runtime

import (
	"io"
	"net"
	"sync"
	"time"

	basetunnel "github.com/alpacax/alpacon-cli/pkg/tunnel"
)

// halfCloseTimeout bounds the direction left open after the other ends cleanly, busy or idle: EOF reads
// the same whether the peer half-closed or left, and smux drops writes to a stream its peer closed.
const halfCloseTimeout = 5 * time.Minute

// relay copies local to remote and back, then closes both before returning. A direction that ends
// cleanly half-closes its destination and gives the other bound to finish; cut reports the bound ended it.
func relay(local, remote net.Conn, bound time.Duration) (cut bool) {
	errChan := make(chan error, 2)
	var pipes sync.WaitGroup
	pipe := func(dst, src net.Conn, copyFn func(io.Writer, io.Reader) (int64, error)) {
		_, err := copyFn(dst, src)
		errChan <- err // the bound starts at EOF, not after a FIN a stalled transport can hold
		if err == nil {
			closeWriteSide(dst)
		}
	}
	pipes.Go(func() { pipe(remote, local, basetunnel.CopyBuffered) })
	pipes.Go(func() { pipe(local, remote, io.Copy) }) // a smux stream's WriteTo writes its own frame buffers

	closeBoth := func() {
		_ = local.Close()
		_ = remote.Close()
	}
	if err := <-errChan; err == nil {
		// A close, not a deadline: smux reads a write deadline only when the write starts.
		timer := time.NewTimer(bound)
		defer timer.Stop()
		select {
		case err := <-errChan:
			if err != nil {
				closeBoth() // before the wait, which a held FIN can stretch to smux's close timeout
			}
		case <-timer.C:
			cut = true
			if tcp, ok := local.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0) // a reset tells the local client its data was cut, not complete
			}
			closeBoth()
			<-errChan
		}
	} else {
		closeBoth()
		<-errChan
	}
	pipes.Wait()
	closeBoth()
	return cut
}

type closeWriter interface {
	CloseWrite() error
}

func closeWriteSide(conn net.Conn) {
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite() // a FIN that fails is left to the bound, not a cut of the other direction
	}
}
