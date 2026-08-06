package event

import (
	"encoding/json"
	"sync"
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
	cmdMu     sync.Mutex // guards commandID
	commandID string
	chunks    chan ChunkEvent
	finished  chan struct{}
}

// commandOutputEnvelope is the WS message format emitted by alpacon-server. One
// struct for both payloads: command_output names the command in command_id,
// command_fin in id.
type commandOutputEnvelope struct {
	EventType EventType `json:"event_type"`
	Payload   struct {
		CommandID string `json:"command_id"`
		Seq       int    `json:"seq"`
		Content   string `json:"content"`
		ID        string `json:"id"`
	} `json:"payload"`
}

// NewCommandOutputListener constructs a listener without connecting. ac may be
// nil (empty header, for tests); commandID may be set later via setCommandID.
func NewCommandOutputListener(ac *client.AlpaconClient, wsURL, commandID string) *CommandOutputListener {
	l := &CommandOutputListener{
		wsListener: newWSListener(ac, wsURL, commandOutputConnectTimeout),
		commandID:  commandID,
		chunks:     make(chan ChunkEvent, commandOutputChunkBuffer),
		finished:   make(chan struct{}, 1),
	}
	l.handleFrame = l.handleMessage
	return l
}

// Chunks returns a receive-only channel of parsed chunk events.
func (l *CommandOutputListener) Chunks() <-chan ChunkEvent { return l.chunks }

// Finished fires when the command reaches a terminal state, which the chunks
// never say: they carry no end marker.
func (l *CommandOutputListener) Finished() <-chan struct{} { return l.finished }

// handleMessage parses one WS frame and routes it: a chunk onto chunks, a fin onto
// finished. Non-matching frames (other event types, another command's id, parse
// failure) are silently dropped.
func (l *CommandOutputListener) handleMessage(raw []byte) {
	var env commandOutputEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}
	l.cmdMu.Lock()
	cid := l.commandID
	l.cmdMu.Unlock()

	switch env.EventType {
	case EventTypeCommandOutput:
		if env.Payload.CommandID != cid {
			return
		}
		select {
		case l.chunks <- ChunkEvent{Seq: env.Payload.Seq, Content: env.Payload.Content}:
		case <-l.done:
		}
	case EventTypeCommandFin:
		// Subscribed per server, so every other command on it lands here too.
		if env.Payload.ID != cid {
			return
		}
		select {
		case l.finished <- struct{}{}:
		default:
		}
	}
}

// setCommandID assigns the commandID after construction. Used because
// SubmitCommand must run after the WS is already connected.
func (l *CommandOutputListener) setCommandID(id string) {
	l.cmdMu.Lock()
	l.commandID = id
	l.cmdMu.Unlock()
}
