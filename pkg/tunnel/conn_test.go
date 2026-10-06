package tunnel

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dialWebSocketServer(tb testing.TB, serve func(*websocket.Conn)) *websocket.Conn {
	tb.Helper()
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			tb.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		serve(conn)
	}))
	tb.Cleanup(ts.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = conn.Close() })
	return conn
}

func writeMessages(tb testing.TB, conn *websocket.Conn, messages ...[]byte) {
	tb.Helper()
	for _, m := range messages {
		if err := conn.WriteMessage(websocket.BinaryMessage, m); err != nil {
			tb.Errorf("write message: %v", err)
			return
		}
	}
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

func TestWebSocketConnReadReturnsMessagesInOrderAcrossSmallBuffers(t *testing.T) {
	t.Parallel()
	first := bytes.Repeat([]byte("a"), 10)
	second := []byte("bcd")
	conn := NewWebSocketConn(dialWebSocketServer(t, func(c *websocket.Conn) {
		writeMessages(t, c, first, second)
	}))

	got := make([]byte, 0, len(first)+len(second))
	buf := make([]byte, 4)
	var err error
	for {
		var n int
		n, err = conn.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}

	assert.Equal(t, slices.Concat(first, second), got)
	assert.True(t, websocket.IsCloseError(err, websocket.CloseNormalClosure), "read must end with the peer's close, got %v", err)
}

func TestWebSocketConnReadSkipsAnEmptyMessage(t *testing.T) {
	t.Parallel()
	conn := NewWebSocketConn(dialWebSocketServer(t, func(c *websocket.Conn) {
		writeMessages(t, c, []byte{}, []byte("abc"))
	}))

	buf := make([]byte, 8)
	n, err := conn.Read(buf)

	require.NoError(t, err)
	assert.Equal(t, "abc", string(buf[:n]))
}

func TestWebSocketConnReadKeepsReturningTheErrorOfAMessageCutShort(t *testing.T) {
	t.Parallel()
	partial := bytes.Repeat([]byte("x"), 8<<10) // past the server's write buffer, so a fragment goes out
	conn := NewWebSocketConn(dialWebSocketServer(t, func(c *websocket.Conn) {
		w, err := c.NextWriter(websocket.BinaryMessage)
		if err != nil {
			t.Errorf("next writer: %v", err)
			return
		}
		if _, err := w.Write(partial); err != nil {
			t.Errorf("write fragment: %v", err)
			return
		}
		_ = c.UnderlyingConn().Close()
	}))

	var got []byte
	buf := make([]byte, 1<<10)
	var err error
	for err == nil {
		var n int
		n, err = conn.Read(buf)
		got = append(got, buf[:n]...)
	}
	_, again := conn.Read(buf)

	assert.NotEmpty(t, got)
	assert.Equal(t, partial[:len(got)], got)
	assert.True(t, websocket.IsCloseError(err, websocket.CloseAbnormalClosure), "read must end with an abnormal closure, got %v", err)
	assert.Equal(t, err, again)
}

// BenchmarkWebSocketConnRead reads the way smux's recvLoop does: header first, then payload.
func BenchmarkWebSocketConnRead(b *testing.B) {
	const header, payload = 8, 32 << 10
	message := make([]byte, header+payload)
	conn := NewWebSocketConn(dialWebSocketServer(b, func(c *websocket.Conn) {
		for c.WriteMessage(websocket.BinaryMessage, message) == nil {
		}
	}))
	hdr, body := make([]byte, header), make([]byte, payload)

	b.SetBytes(int64(len(message)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(conn, body); err != nil {
			b.Fatal(err)
		}
	}
}
