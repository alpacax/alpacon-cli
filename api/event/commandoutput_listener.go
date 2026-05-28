package event

import (
	"encoding/json"
	"fmt"
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

// NewCommandOutputListener constructs a listener without connecting.
// ac is reserved for header-building; pass nil to use the empty header
// (useful in tests). commandID may be empty at construction time and set
// later via setCommandID once SubmitCommand returns.
func NewCommandOutputListener(ac *client.AlpaconClient, wsURL, commandID string) *CommandOutputListener {
	var header http.Header
	if ac != nil {
		header = ac.SetWebsocketHeader()
	} else {
		header = http.Header{}
	}
	return &CommandOutputListener{
		ac:        ac,
		wsURL:     wsURL,
		wsHeader:  header,
		commandID: commandID,
		chunks:    make(chan ChunkEvent, commandOutputChunkBuffer),
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		connected: make(chan struct{}),
	}
}

// Start begins the WebSocket receive loop in a background goroutine.
// It automatically reconnects with exponential backoff until Stop() is called.
func (l *CommandOutputListener) Start() {
	go func() {
		defer close(l.stopped)
		l.listenLoop()
	}()
}

// Chunks returns a receive-only channel of parsed chunk events.
func (l *CommandOutputListener) Chunks() <-chan ChunkEvent { return l.chunks }

// Done returns a channel closed when the listener has fully stopped.
func (l *CommandOutputListener) Done() <-chan struct{} { return l.stopped }

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

		connected, _ := l.connectAndListen()
		if connected {
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

func (l *CommandOutputListener) connectAndListen() (connected bool, err error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, dialErr := dialer.Dial(l.wsURL, l.wsHeader)
	if dialErr != nil {
		return false, fmt.Errorf("event websocket connection failed: %w", dialErr)
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
			return true, nil
		default:
		}
		_, message, readErr := conn.ReadMessage()
		if readErr != nil {
			select {
			case <-l.done:
				return true, nil
			default:
			}
			return true, fmt.Errorf("event websocket read error: %w", readErr)
		}
		l.handleMessage(message)
	}
}
