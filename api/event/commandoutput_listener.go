package event

import (
	"encoding/json"
	"time"

	"github.com/alpacax/alpacon-cli/client"
)

const (
	commandOutputChunkBuffer = 256

	// commandOutputConnectTimeout bounds the dial and doubles as the callers'
	// WaitConnected budget, so a slow dial can't outlive the wait.
	commandOutputConnectTimeout = 5 * time.Second
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
	*wsListener
	commandID string
	chunks    chan ChunkEvent
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

// NewCommandOutputListener constructs a listener without connecting. ac may be
// nil (empty header, for tests); commandID may be set later via setCommandID.
func NewCommandOutputListener(ac *client.AlpaconClient, wsURL, commandID string) *CommandOutputListener {
	l := &CommandOutputListener{
		wsListener: newWSListener(ac, wsURL, commandOutputConnectTimeout),
		commandID:  commandID,
		chunks:     make(chan ChunkEvent, commandOutputChunkBuffer),
	}
	l.handleFrame = l.handleMessage
	return l
}

// Chunks returns a receive-only channel of parsed chunk events.
func (l *CommandOutputListener) Chunks() <-chan ChunkEvent { return l.chunks }

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
	l.mu.Lock()
	cid := l.commandID
	l.mu.Unlock()
	if env.Payload.CommandID != cid {
		return
	}

	select {
	case l.chunks <- ChunkEvent{Seq: env.Payload.Seq, Content: env.Payload.Content}:
	case <-l.done:
	}
}

// setCommandID assigns the commandID after construction. Used because
// SubmitCommand must run after the WS is already connected.
func (l *CommandOutputListener) setCommandID(id string) {
	l.mu.Lock()
	l.commandID = id
	l.mu.Unlock()
}
