package event

import (
	"net/http"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/gorilla/websocket"
)

const (
	wsReconnectBaseDelay = 1 * time.Second
	wsReconnectMaxDelay  = 30 * time.Second
)

// wsListener is the shared dial/reconnect/shutdown skeleton for event WebSocket
// consumers; embedders must assign handleFrame before calling Start.
type wsListener struct {
	// Event channel tokens are single-use, so a reconnect needs a freshly provisioned URL.
	provision func() (string, error)
	// The server accepts a subscription only once the channel is connected.
	onConnected func() error
	// A dial error reaches neither provision nor onConnected, so without this an outage
	// at the transport level is silent.
	onDialFailed     func(error)
	handleFrame      func(message []byte)
	wsHeader         http.Header
	handshakeTimeout time.Duration
	readLimit        int64 // 0 for gorilla's default of no limit
	// The first backoff step, lowered by tests so a reconnect assertion does not have to
	// sit through the production delay.
	reconnectBaseDelay time.Duration

	done        chan struct{}
	stopped     chan struct{} // closed when listenLoop exits
	connected   chan struct{} // closed once a dial connected and onConnected returned
	connectOnce sync.Once
	closeOnce   sync.Once
	mu          sync.Mutex // guards conn
	conn        *websocket.Conn
}

// newProvisionedWSListener builds the skeleton for a listener that obtains a new
// URL for every dial attempt. A nil ac only means an empty header here; whether it
// is safe at all is provision's call, since every attempt runs it.
func newProvisionedWSListener(ac *client.AlpaconClient, provision func() (string, error), handshakeTimeout time.Duration) *wsListener {
	wsHeader := http.Header{}
	if ac != nil {
		wsHeader = ac.SetWebsocketHeader()
	}
	return &wsListener{
		provision:          provision,
		wsHeader:           wsHeader,
		handshakeTimeout:   handshakeTimeout,
		reconnectBaseDelay: wsReconnectBaseDelay,
		done:               make(chan struct{}),
		stopped:            make(chan struct{}),
		connected:          make(chan struct{}),
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

// WaitConnected blocks until a dial has connected and its onConnected hook has
// returned, until timeout, or until shutdown; returns whether it connected. A hook
// with nothing to subscribe to still counts, so this promises no subscription.
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
	delay := w.reconnectBaseDelay

	for {
		select {
		case <-w.done:
			return
		default:
		}

		// Reset backoff if we had a successful connection that later dropped
		if w.connectAndListen() {
			delay = w.reconnectBaseDelay
		}

		select {
		case <-w.done:
			return
		case <-time.After(delay):
			delay = nextReconnectDelay(delay)
		}
	}
}

// nextReconnectDelay doubles the backoff first, so the cap is a ceiling on the
// delay actually waited rather than on the value that gets doubled.
func nextReconnectDelay(delay time.Duration) time.Duration {
	if delay *= 2; delay > wsReconnectMaxDelay {
		return wsReconnectMaxDelay
	}
	return delay
}

// connectAndListen dials the event WebSocket, runs onConnected if set, then reads
// until the connection drops or Stop is called. Returns whether it connected and
// subscribed, so the caller can reset backoff.
func (w *wsListener) connectAndListen() (connected bool) {
	wsURL, err := w.provision()
	if err != nil {
		return false
	}

	dialer := websocket.Dialer{HandshakeTimeout: w.handshakeTimeout}
	conn, _, dialErr := dialer.Dial(wsURL, w.wsHeader)
	if dialErr != nil {
		if w.onDialFailed != nil {
			w.onDialFailed(dialErr)
		}
		return false
	}

	if w.readLimit > 0 {
		conn.SetReadLimit(w.readLimit)
	}

	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.conn = nil
		w.mu.Unlock()
		_ = conn.Close()
	}()

	if w.onConnected != nil {
		if err := w.onConnected(); err != nil {
			return false
		}
	}

	w.connectOnce.Do(func() { close(w.connected) })

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

// isFatalRequestError reports whether retrying is pointless: a 4xx that refuses rather
// than asks to be retried is a bad request or an expired login, unchanged by a retry.
func isFatalRequestError(cause error) bool {
	return utils.IsFatalClientError(utils.HTTPStatusCode(cause))
}
