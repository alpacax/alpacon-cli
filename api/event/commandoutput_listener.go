package event

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/gorilla/websocket"
)

// ChunkEvent is a single command_output chunk emitted by the listener.
type ChunkEvent struct {
	Seq     int
	Content string
}

// CommandOutputListener subscribes to a single command's chunk stream over
// the event WebSocket and exposes received chunks via the Chunks() channel.
//
// Lifecycle: NewCommandOutputListener -> Start -> (consume Chunks) -> Stop.
// Stop is idempotent and safe to call from any goroutine.
type CommandOutputListener struct {
	ac          *client.AlpaconClient
	wsURL       string
	wsHeader    http.Header
	commandID   string
	chunks      chan ChunkEvent
	done        chan struct{}
	stopped     chan struct{}
	connected   chan struct{}
	connectOnce sync.Once
	closeOnce   sync.Once
	mu          sync.Mutex
	conn        *websocket.Conn
}

// commandOutputEnvelope is the WS message format emitted by alpacon-server.
type commandOutputEnvelope struct {
	EventType string `json:"event_type"`
	Payload   struct {
		CommandID string `json:"command_id"`
		Seq       int    `json:"seq"`
		Content   string `json:"content"`
	} `json:"payload"`
}

// handleMessage parses one WS frame and pushes a matching chunk onto chunks.
// Non-matching frames (wrong event_type, wrong command_id, parse failure) are
// silently dropped.
func (l *CommandOutputListener) handleMessage(raw []byte) {
	var env commandOutputEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}
	if env.EventType != "command_output" {
		return
	}
	if env.Payload.CommandID != l.commandID {
		return
	}

	select {
	case l.chunks <- ChunkEvent{Seq: env.Payload.Seq, Content: env.Payload.Content}:
	case <-l.done:
	}
}
