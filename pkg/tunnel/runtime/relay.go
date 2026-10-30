package runtime

import (
	"net"
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
	pipe := func(dst, src net.Conn) {
		_, err := basetunnel.CopyBuffered(dst, src)
		if err == nil {
			err = closeWriteSide(dst)
		}
		errChan <- err
	}
	go pipe(remote, local)
	go pipe(local, remote)

	if err := <-errChan; err == nil {
		// A close, not a deadline: smux reads a write deadline only when the write starts.
		timer := time.NewTimer(bound)
		defer timer.Stop()
		select {
		case <-errChan:
		case <-timer.C:
			cut = true
			if tcp, ok := local.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0) // a reset tells the local client its data was cut, not complete
			}
			_ = local.Close()
			_ = remote.Close()
			<-errChan
		}
	} else {
		_ = local.Close()
		_ = remote.Close()
		<-errChan
	}
	_ = local.Close()
	_ = remote.Close()
	return cut
}

func closeWriteSide(conn net.Conn) error {
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
