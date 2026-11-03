package testutil

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWebSocketServer_HandlerEndsBeforeTheTestReturns(t *testing.T) {
	t.Parallel()
	var returned atomic.Bool
	var client *websocket.Conn
	t.Run("server", func(t *testing.T) {
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
					return
				}
			}
		}))
		var err error
		client, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
		require.NoError(t, err)
	})
	defer func() { _ = client.Close() }()

	assert.True(t, returned.Load(), "handler still reading after the subtest's cleanup, with the client still open")
}
