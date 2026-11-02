package testutil

import (
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestPipeServer_CloseEndsAHijackedConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ended := make(chan struct{})
		upgrader := websocket.Upgrader{}
		s := NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer close(ended)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}))
		dialer := websocket.Dialer{NetDialContext: s.DialContext}
		client, _, err := dialer.Dial(s.WebsocketURL("/"), nil)
		require.NoError(t, err)
		defer func() { _ = client.Close() }()

		s.Close()
		synctest.Wait()

		select {
		case <-ended:
		default:
			t.Fatal("the hijacked connection's handler is still reading after Close")
		}
	})
}
