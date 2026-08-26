package event

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandOutputListener_HandleMessage_FiltersAndEmits(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantChunk *ChunkEvent // nil = expect no emission
	}{
		{
			name:      "matching command_output",
			payload:   `{"event_type":"command_output","payload":{"command_id":"cmd-1","seq":3,"content":"hi"}}`,
			wantChunk: &ChunkEvent{Seq: 3, Content: "hi"},
		},
		{
			name:    "wrong event_type",
			payload: `{"event_type":"server_status","payload":{"command_id":"cmd-1","seq":3,"content":"hi"}}`,
		},
		{
			name:    "wrong command_id",
			payload: `{"event_type":"command_output","payload":{"command_id":"cmd-OTHER","seq":3,"content":"hi"}}`,
		},
		{
			name:    "invalid json",
			payload: `not json`,
		},
		{
			name:    "empty payload",
			payload: `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewCommandOutputListener(nil)
			l.setTargets("cmd-1", "")
			l.handleMessage([]byte(tt.payload))

			select {
			case got := <-l.chunks:
				if tt.wantChunk == nil {
					t.Fatalf("expected no emission, got %+v", got)
				}
				assert.Equal(t, *tt.wantChunk, got)
			case <-time.After(50 * time.Millisecond):
				if tt.wantChunk != nil {
					t.Fatal("expected emission but got nothing")
				}
			}
		})
	}
}

// The fin subscription targets the server, so every command on it reports here.
func TestCommandOutputListener_HandleMessage_FinishedIsPerCommand(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		wantFinished bool
	}{
		{
			name:         "fin for this command",
			payload:      `{"event_type":"command_fin","payload":{"type":"event","id":"cmd-1"}}`,
			wantFinished: true,
		},
		{
			name:    "fin for another command on the same server",
			payload: `{"event_type":"command_fin","payload":{"type":"event","id":"cmd-OTHER"}}`,
		},
		{
			name:    "chunk does not finish the command",
			payload: `{"event_type":"command_output","payload":{"command_id":"cmd-1","seq":0,"content":"hi"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewCommandOutputListener(nil)
			l.setTargets("cmd-1", "")
			l.handleMessage([]byte(tt.payload))

			select {
			case <-l.Finished():
				if !tt.wantFinished {
					t.Fatal("expected no finish signal")
				}
			case <-time.After(50 * time.Millisecond):
				if tt.wantFinished {
					t.Fatal("expected a finish signal but got nothing")
				}
			}
		})
	}
}

func TestCommandOutputListener_Start_DeliversChunks(t *testing.T) {
	cmdID := "cmd-uuid"

	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		defer func() { _ = conn.Close() }()

		for _, c := range []ChunkEvent{{Seq: 0, Content: "a"}, {Seq: 1, Content: "b"}} {
			env := map[string]any{
				"event_type": "command_output",
				"payload": map[string]any{
					"command_id": cmdID,
					"seq":        c.Seq,
					"content":    c.Content,
				},
			}
			b, _ := json.Marshal(env)
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}

		// Block until client disconnects
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	l := NewCommandOutputListener(ac)
	l.setTargets(cmdID, "")
	l.Start()
	defer l.Stop()

	require.True(t, l.WaitConnected(2*time.Second), "the listener must connect before any chunk can arrive")

	got := []ChunkEvent{}
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case c := <-l.Chunks():
			got = append(got, c)
		case <-timeout:
			t.Fatalf("timeout, got %+v", got)
		}
	}

	assert.Equal(t, []ChunkEvent{{Seq: 0, Content: "a"}, {Seq: 1, Content: "b"}}, got)
}

func TestCommandOutputListener_Reconnects(t *testing.T) {
	cmdID := "cmd-uuid"

	ts, sessions, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			// First connection: emit one chunk and drop
			env := `{"event_type":"command_output","payload":{"command_id":"` + cmdID + `","seq":0,"content":"first"}}`
			_ = conn.WriteMessage(websocket.TextMessage, []byte(env))
			_ = conn.Close()
			return
		}
		// Second connection: emit second chunk and block
		env := `{"event_type":"command_output","payload":{"command_id":"` + cmdID + `","seq":1,"content":"second"}}`
		_ = conn.WriteMessage(websocket.TextMessage, []byte(env))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	l := NewCommandOutputListener(ac)
	l.setTargets(cmdID, "")
	l.reconnectBaseDelay = testReconnectBaseDelay
	l.Start()
	defer l.Stop()

	require.True(t, l.WaitConnected(2*time.Second), "the first dial must connect before a reconnect can be observed")

	got := []ChunkEvent{}
	timeout := time.After(5 * time.Second)
	for len(got) < 2 {
		select {
		case c := <-l.Chunks():
			got = append(got, c)
		case <-timeout:
			t.Fatalf("timeout, got %+v (sessions=%d)", got, sessions.Load())
		}
	}

	assert.Equal(t, ChunkEvent{Seq: 0, Content: "first"}, got[0])
	assert.Equal(t, ChunkEvent{Seq: 1, Content: "second"}, got[1])
	assert.GreaterOrEqual(t, sessions.Load(), int32(2))
}

func TestCommandOutputListener_CreatesANewSessionAndResubscribesOnReconnect(t *testing.T) {
	ts, sessions, subscribes := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			// The token is single-use, so recovering means a whole new session.
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	l := NewCommandOutputListener(ac)
	l.reconnectBaseDelay = testReconnectBaseDelay
	l.Start()
	defer l.Stop()

	require.True(t, l.WaitConnected(3*time.Second), "the first dial must connect before the reconnect")
	require.NoError(t, l.subscribeTo("cmd-1", "srv-1"))

	assert.Eventually(t, func() bool {
		return sessions.Load() >= 2 && subscribes.Load() >= 3
	}, 5*time.Second, 10*time.Millisecond, "a reconnect must create a new session and re-issue both subscriptions")
}

func TestCommandOutputListener_FirstConnectSubscribesToNothing(t *testing.T) {
	ts, _, subscribes := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	l := NewCommandOutputListener(ac)
	l.Start()
	defer l.Stop()

	require.True(t, l.WaitConnected(3*time.Second), "the first dial must connect even with nothing to subscribe to")
	assert.Equal(t, int32(0), subscribes.Load())
}

func TestCommandOutputListener_SurfacesAFatalSessionFailure(t *testing.T) {
	ts, _, _ := newWatcherTestServer(t, alwaysRejected, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	l := NewCommandOutputListener(ac)
	l.Start()
	defer l.Stop()

	// A rejected session ends the wait instead of retrying, and the caller needs the
	// server's own reason for its fallback.
	assert.False(t, l.WaitConnected(commandOutputConnectTimeout), "a rejected session must not report a connection")

	err := l.Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create event session")
}

func TestListenerFailureFallsBackToTheConnectBudget(t *testing.T) {
	assert.Contains(t, listenerFailure(NewCommandOutputListener(nil)).Error(), "connect timeout")
}

func TestCommandOutputListener_KeepsRetryingAfterTheFirstSubscribe(t *testing.T) {
	rejectSecond := func(attempt int32) int {
		if attempt == 2 {
			return http.StatusUnauthorized
		}
		return http.StatusCreated
	}

	subscribed := make(chan struct{})
	ts, sessions, _ := newWatcherTestServer(t, rejectSecond, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			// Hold the first connection until the caller has subscribed, so the
			// rejected reconnect lands on a listener that is already streaming.
			<-subscribed
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	l := NewCommandOutputListener(ac)
	l.reconnectBaseDelay = testReconnectBaseDelay
	l.Start()
	defer l.Stop()

	require.True(t, l.WaitConnected(3*time.Second), "the first dial must connect")
	require.NoError(t, l.subscribeTo("cmd-1", "srv-1"))
	close(subscribed)

	assert.Eventually(t, func() bool {
		return sessions.Load() >= 3
	}, 5*time.Second, 10*time.Millisecond, "a rejected reconnect must not end a listener that already subscribed")
}
