package event

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
			l := &CommandOutputListener{
				commandID: "cmd-1",
				chunks:    make(chan ChunkEvent, 1),
				done:      make(chan struct{}),
			}
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

func TestCommandOutputListener_Start_DeliversChunks(t *testing.T) {
	upgrader := websocket.Upgrader{}
	cmdID := "cmd-uuid"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Emit two chunks
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
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	l := NewCommandOutputListener(nil, wsURL, cmdID)
	l.Start()
	defer l.Stop()

	require.True(t, l.WaitConnected(2*time.Second), "should connect")

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

func TestCommandOutputListener_StopIsIdempotent(t *testing.T) {
	l := &CommandOutputListener{
		chunks:    make(chan ChunkEvent, 1),
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		connected: make(chan struct{}),
	}
	l.Stop()
	l.Stop()
	l.Stop()
}
