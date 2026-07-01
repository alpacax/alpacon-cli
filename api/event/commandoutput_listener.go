package event

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/gorilla/websocket"
)

const (
	// Mirror sudolistener backoff to keep reconnection behavior consistent.
	commandOutputReconnectBase = 1 * time.Second
	commandOutputReconnectMax  = 30 * time.Second
	commandOutputChunkBuffer   = 256
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
	wsURL       string
	wsHeader    http.Header
	commandID   string
	chunks      chan ChunkEvent
	done        chan struct{}
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

// NewCommandOutputListener constructs a listener without connecting. ac may be
// nil (empty header, for tests); commandID may be set later via setCommandID.
func NewCommandOutputListener(ac *client.AlpaconClient, wsURL, commandID string) *CommandOutputListener {
	var header http.Header
	if ac != nil {
		header = ac.SetWebsocketHeader()
	} else {
		header = http.Header{}
	}
	return &CommandOutputListener{
		wsURL:     wsURL,
		wsHeader:  header,
		commandID: commandID,
		chunks:    make(chan ChunkEvent, commandOutputChunkBuffer),
		done:      make(chan struct{}),
		connected: make(chan struct{}),
	}
}

// Start begins the WebSocket receive loop in a background goroutine.
// It automatically reconnects with exponential backoff until Stop() is called.
func (l *CommandOutputListener) Start() {
	go l.listenLoop()
}

// Chunks returns a receive-only channel of parsed chunk events.
func (l *CommandOutputListener) Chunks() <-chan ChunkEvent { return l.chunks }

// WaitConnected blocks until the first successful WS connection is made or
// timeout / shutdown intervenes. Returns true on success.
func (l *CommandOutputListener) WaitConnected(timeout time.Duration) bool {
	select {
	case <-l.connected:
		return true
	case <-l.done:
		return false
	case <-time.After(timeout):
		return false
	}
}

// Stop closes the listener; safe to call multiple times and from any goroutine.
func (l *CommandOutputListener) Stop() {
	l.closeOnce.Do(func() {
		close(l.done)
		l.mu.Lock()
		if l.conn != nil {
			_ = l.conn.Close()
		}
		l.mu.Unlock()
	})
}

func (l *CommandOutputListener) listenLoop() {
	delay := commandOutputReconnectBase
	for {
		select {
		case <-l.done:
			return
		default:
		}

		if l.connectAndListen() {
			delay = commandOutputReconnectBase
		}

		select {
		case <-l.done:
			return
		case <-time.After(delay):
			delay *= 2
			if delay > commandOutputReconnectMax {
				delay = commandOutputReconnectMax
			}
		}
	}
}

// connectAndListen dials, reads frames until the connection drops or Stop is
// called, and returns whether a connection was established (to reset backoff).
func (l *CommandOutputListener) connectAndListen() (connected bool) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, dialErr := dialer.Dial(l.wsURL, l.wsHeader)
	if dialErr != nil {
		return false
	}

	l.mu.Lock()
	l.conn = conn
	l.mu.Unlock()

	l.connectOnce.Do(func() { close(l.connected) })

	defer func() {
		l.mu.Lock()
		l.conn = nil
		l.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		select {
		case <-l.done:
			return true
		default:
		}
		_, message, readErr := conn.ReadMessage()
		if readErr != nil {
			return true
		}
		l.handleMessage(message)
	}
}
