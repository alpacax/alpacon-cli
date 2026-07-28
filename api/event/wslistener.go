package event

import (
	"net/http"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/gorilla/websocket"
)

const (
	wsReconnectBaseDelay = 1 * time.Second
	wsReconnectMaxDelay  = 30 * time.Second
)

// wsListener is the shared dial/reconnect/shutdown skeleton for event WebSocket
// consumers; embedders must assign handleFrame before calling Start.
type wsListener struct {
	wsURL            string
	wsHeader         http.Header
	handshakeTimeout time.Duration
	handleFrame      func(message []byte)

	done        chan struct{}
	stopped     chan struct{} // closed when listenLoop exits
	connected   chan struct{} // closed after first successful WebSocket connection
	connectOnce sync.Once
	closeOnce   sync.Once
	mu          sync.Mutex // guards conn; embedders may reuse it for their own state
	conn        *websocket.Conn
}

// newWSListener builds the shared skeleton. ac may be nil (empty header, for tests).
func newWSListener(ac *client.AlpaconClient, wsURL string, handshakeTimeout time.Duration) wsListener {
	wsHeader := http.Header{}
	if ac != nil {
		wsHeader = ac.SetWebsocketHeader()
	}
	return wsListener{
		wsURL:            wsURL,
		wsHeader:         wsHeader,
		handshakeTimeout: handshakeTimeout,
		done:             make(chan struct{}),
		stopped:          make(chan struct{}),
		connected:        make(chan struct{}),
	}
}

// Start reads events in a background goroutine, reconnecting automatically
// until Stop is called.
func (w *wsListener) Start() {
	// Fail here rather than in the read loop, where the nil call would only
	// surface after a successful dial, on another goroutine.
	if w.handleFrame == nil {
		panic("event: wsListener.handleFrame must be assigned before Start")
	}

	go func() {
		defer close(w.stopped)
		w.listenLoop()
	}()
}

// WaitConnected blocks until connected, timeout, or shutdown; returns whether
// it connected.
func (w *wsListener) WaitConnected(timeout time.Duration) bool {
	select {
	case <-w.connected:
		return true
	case <-w.done:
		return false
	case <-time.After(timeout):
		return false
	}
}

// Stop shuts down the listener and closes the connection to unblock a pending
// ReadMessage. Safe to call from any goroutine.
func (w *wsListener) Stop() {
	w.closeOnce.Do(func() {
		close(w.done)
		w.mu.Lock()
		if w.conn != nil {
			_ = w.conn.Close()
		}
		w.mu.Unlock()
	})
}

func (w *wsListener) listenLoop() {
	delay := wsReconnectBaseDelay

	for {
		select {
		case <-w.done:
			return
		default:
		}

		// Reset backoff if we had a successful connection that later dropped
		if w.connectAndListen() {
			delay = wsReconnectBaseDelay
		}

		select {
		case <-w.done:
			return
		case <-time.After(delay):
			delay *= 2
			if delay > wsReconnectMaxDelay {
				delay = wsReconnectMaxDelay
			}
		}
	}
}

// connectAndListen dials the event WebSocket and reads until it drops or Stop
// is called; returns whether it connected, so the caller can reset backoff.
func (w *wsListener) connectAndListen() (connected bool) {
	dialer := websocket.Dialer{HandshakeTimeout: w.handshakeTimeout}
	conn, _, dialErr := dialer.Dial(w.wsURL, w.wsHeader)
	if dialErr != nil {
		return false
	}

	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()

	w.connectOnce.Do(func() { close(w.connected) })

	defer func() {
		w.mu.Lock()
		w.conn = nil
		w.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		select {
		case <-w.done:
			return true
		default:
		}

		_, message, readErr := conn.ReadMessage()
		if readErr != nil {
			return true
		}

		w.handleFrame(message)
	}
}
