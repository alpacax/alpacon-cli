package tunnel

import (
	"io"

	"github.com/gorilla/websocket"
)

// WebSocketConn wraps a WebSocket connection to implement io.ReadWriteCloser interface.
type WebSocketConn struct {
	conn       *websocket.Conn
	readBuffer []byte
}

func NewWebSocketConn(conn *websocket.Conn) *WebSocketConn {
	return &WebSocketConn{conn: conn}
}

func (w *WebSocketConn) Read(b []byte) (int, error) {
	if len(w.readBuffer) > 0 {
		n := copy(b, w.readBuffer)
		w.readBuffer = w.readBuffer[n:]
		return n, nil
	}

	_, msg, err := w.conn.ReadMessage()
	if err != nil {
		return 0, err
	}

	n := copy(b, msg)
	if n < len(msg) {
		w.readBuffer = msg[n:]
	}

	return n, nil
}

func (w *WebSocketConn) Write(b []byte) (int, error) {
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
