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

// CommandOutputListener subscribes to a single command's chunk stream over the
// event WebSocket, exposing chunks on Chunks() and the command's fin on
// Finished().
//
// Lifecycle: NewCommandOutputListener -> Start -> subscribeTo -> (consume Chunks) -> Stop.
// Stop is idempotent and safe to call from any goroutine. Event channel tokens are
// single-use, so every dial provisions its own session and re-subscribes.
type CommandOutputListener struct {
	*wsListener
	ac        *client.AlpaconClient
	cmdMu     sync.Mutex // guards commandID, serverID
	commandID string
	serverID  string
	stateMu   sync.Mutex // guards channelID, err
	channelID string
	err       error
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
// nil (empty header, for tests); the targets are set later via setTargets, because
// SubmitCommand must run once the WS is already connected.
func NewCommandOutputListener(ac *client.AlpaconClient) *CommandOutputListener {
	l := &CommandOutputListener{
		ac:       ac,
		chunks:   make(chan ChunkEvent, commandOutputChunkBuffer),
		finished: make(chan struct{}, 1),
	}
	l.wsListener = newProvisionedWSListener(ac, l.provisionSession, commandOutputConnectTimeout)
	l.handleFrame = l.handleMessage
	l.onConnected = l.subscribe
	return l
}

// Err returns why the listener could not connect, so a caller that only sees
// WaitConnected time out can still report the server's own reason.
func (l *CommandOutputListener) Err() error {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	return l.err
}

func (l *CommandOutputListener) provisionSession() (string, error) {
	session, err := CreateEventSession(l.ac)
	if err != nil {
		l.stateMu.Lock()
		l.err = err
		l.stateMu.Unlock()
		// A 4xx means a bad request or an expired login, so retrying only burns the
		// caller's connect budget before it falls back to polling anyway.
		if isFatalRequestError(err) {
			l.Stop()
		}
		return "", err
	}

	l.stateMu.Lock()
	l.channelID = session.ChannelID
	l.err = nil
	l.stateMu.Unlock()

	return session.WebsocketURL, nil
}

// subscribeTo issues the first subscription, once the command exists. Every later
// connect re-issues it through onConnected.
func (l *CommandOutputListener) subscribeTo(commandID, serverID string) error {
	l.setTargets(commandID, serverID)
	return l.subscribe()
}

func (l *CommandOutputListener) subscribe() error {
	l.cmdMu.Lock()
	commandID, serverID := l.commandID, l.serverID
	l.cmdMu.Unlock()

	// The command is submitted only after the WS is up, so the first connect has
	// nothing to subscribe to yet.
	if commandID == "" {
		return nil
	}

	l.stateMu.Lock()
	channelID := l.channelID
	l.stateMu.Unlock()

	if err := SubscribeEvent(l.ac, channelID, EventTypeCommandOutput, commandID); err != nil {
		return err
	}

	// Chunks carry no end marker, so without this the exit waits for the poll to
	// notice. Best effort: on failure that poll stays the only terminal signal.
	if serverID != "" {
		_ = SubscribeEvent(l.ac, channelID, EventTypeCommandFin, serverID)
	}

	return nil
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

// setTargets names the command whose chunks to keep and the server whose fin event
// ends the stream.
func (l *CommandOutputListener) setTargets(commandID, serverID string) {
	l.cmdMu.Lock()
	l.commandID = commandID
	l.serverID = serverID
	l.cmdMu.Unlock()
}
