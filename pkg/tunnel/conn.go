package tunnel

import (
	"io"
	"sync"

	"github.com/gorilla/websocket"
)

// WebSocketConn wraps a WebSocket connection to implement io.ReadWriteCloser interface.
type WebSocketConn struct {
	conn    *websocket.Conn
	message io.Reader
	writeMu sync.Mutex
}

func NewWebSocketConn(conn *websocket.Conn) *WebSocketConn {
	return &WebSocketConn{conn: conn}
}

func (w *WebSocketConn) Read(b []byte) (int, error) {
	for {
		if w.message == nil {
			_, r, err := w.conn.NextReader()
			if err != nil {
				return 0, err
			}
			w.message = r
		}

		n, err := w.message.Read(b)
		if err != io.EOF {
			return n, err
		}
		w.message = nil
		if n > 0 {
			return n, nil
		}
	}
}

func (w *WebSocketConn) Write(b []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	err := w.conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (w *WebSocketConn) Close() error {
	return w.conn.Close()
}

var _ io.ReadWriteCloser = (*WebSocketConn)(nil)
