package tunnel

import (
	"io"
	"sync"

	"github.com/gorilla/websocket"
)

// maxTextFrameSize bounds a text frame read for its handler.
const maxTextFrameSize = 64 << 10

// WebSocketConn wraps a WebSocket connection to implement io.ReadWriteCloser interface.
// Binary frames carry the stream; text frames are control messages beside it.
type WebSocketConn struct {
	conn    *websocket.Conn
	message io.Reader
	writeMu sync.Mutex
	onText  func([]byte)
}

func NewWebSocketConn(conn *websocket.Conn) *WebSocketConn {
	return &WebSocketConn{conn: conn}
}

// NewWebSocketConnWithText is NewWebSocketConn that hands each text frame to
// onText, on the reading goroutine.
func NewWebSocketConnWithText(conn *websocket.Conn, onText func([]byte)) *WebSocketConn {
	return &WebSocketConn{conn: conn, onText: onText}
}

func (w *WebSocketConn) Read(b []byte) (int, error) {
	for {
		if w.message == nil {
			kind, r, err := w.conn.NextReader()
			if err != nil {
				return 0, err
			}
			if kind == websocket.TextMessage {
				text, err := io.ReadAll(io.LimitReader(r, maxTextFrameSize))
				if err != nil {
					return 0, err
				}
				if w.onText != nil {
					w.onText(text)
				}
				continue
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
