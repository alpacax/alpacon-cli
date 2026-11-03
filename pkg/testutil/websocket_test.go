package testutil

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWebSocketServer_HandlerEndsBeforeTheTestReturns(t *testing.T) {
	t.Parallel()
	var returned atomic.Bool
	t.Run("client left open", func(t *testing.T) {
		var client *websocket.Conn
		t.Cleanup(func() { // registered first, so it runs after the server's cleanup
			if client != nil {
				_ = client.Close()
			}
		})
		upgrader := websocket.Upgrader{}
		ts := NewWebSocketServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer returned.Store(true)
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					time.Sleep(50 * time.Millisecond) // a cleanup that skips the wait would now always see the handler running
					return
				}
			}
		}))
		var err error
		client, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
		require.NoError(t, err)
	})

	assert.True(t, returned.Load(), "handler still reading after the subtest's cleanup, with the client still open")
}
