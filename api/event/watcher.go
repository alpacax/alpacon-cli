package event

import (
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/client"
)

const (
	watchHandshakeTimeout = 10 * time.Second
	watchFrameBuffer      = 256

	// Bounded so a compromised server cannot grow the CLI's memory without limit.
	watchFrameLimit = 1024 * 1024
)

// Watcher streams raw event frames for one event type. Event channel tokens are
// single-use, so every dial attempt provisions its own session and re-subscribes once
// connected. When WaitConnected returns false, Err holds the reason if it was fatal.
type Watcher struct {
	*wsListener
	ac              *client.AlpaconClient
	eventType       EventType
	targetID        string
	frames          chan []byte
	reconnected     chan struct{}
	reconnectFailed chan error

	stateMu    sync.Mutex // guards channelID, subscribed, warned, err
	channelID  string
	subscribed bool
	warned     bool
	err        error
}

func NewWatcher(ac *client.AlpaconClient, eventType EventType, targetID string) *Watcher {
	w := &Watcher{
		ac:              ac,
		eventType:       eventType,
		targetID:        targetID,
		frames:          make(chan []byte, watchFrameBuffer),
		reconnected:     make(chan struct{}, 1),
		reconnectFailed: make(chan error, 1),
	}
	w.wsListener = newProvisionedWSListener(ac, w.provisionSession, watchHandshakeTimeout)
	w.readLimit = watchFrameLimit
	w.handleFrame = w.handleMessage
	w.onConnected = w.subscribe
	w.onDialFailed = w.announceOutage
	return w
}

// Only after a first success: before that, a dial is worth retrying inside
// WaitConnected's window rather than ending the command.
func (w *Watcher) announceOutage(cause error) {
	w.stateMu.Lock()
	subscribed := w.subscribed
	w.stateMu.Unlock()

	if subscribed {
		_ = w.fail(cause)
	}
}

func (w *Watcher) Frames() <-chan []byte { return w.frames }

// Reconnected fires on each re-subscribe. Events published while disconnected are
// lost for good: the event channel has no history to replay.
func (w *Watcher) Reconnected() <-chan struct{} { return w.reconnected }

// ReconnectFailed reports the first failure of each outage, so a stream whose
// reconnects keep failing is distinguishable from a quiet one.
func (w *Watcher) ReconnectFailed() <-chan error { return w.reconnectFailed }

// Err returns the error that stopped the Watcher before it ever subscribed.
func (w *Watcher) Err() error {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	return w.err
}

func (w *Watcher) provisionSession() (string, error) {
	session, err := CreateEventSession(w.ac)
	if err != nil {
		return "", w.fail(err)
	}

	// Rejected here because the dialer's own *url.Error quotes the whole URL, and this
	// URL carries a live channel token. Only the inner reason is safe to keep.
	if _, err := url.Parse(session.WebsocketURL); err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return "", w.fail(fmt.Errorf("server returned a malformed event channel URL: %w", err))
	}

	w.stateMu.Lock()
	w.channelID = session.ChannelID
	w.stateMu.Unlock()

	return session.WebsocketURL, nil
}

func (w *Watcher) subscribe() error {
	w.stateMu.Lock()
	channelID := w.channelID
	first := !w.subscribed
	w.stateMu.Unlock()

	if err := SubscribeEvent(w.ac, channelID, w.eventType, w.targetID); err != nil {
		return w.fail(err)
	}

	w.stateMu.Lock()
	w.subscribed = true
	w.warned = false
	// An unread notice describes an outage that is over: leaving it queued would
	// misreport a healthy stream and crowd out the next outage's notice.
	select {
	case <-w.reconnectFailed:
	default:
	}
	w.stateMu.Unlock()

	if !first {
		select {
		case w.reconnected <- struct{}{}:
		default:
		}
	}

	return nil
}

// A failure before any successful subscribe is fatal, so a bad target or an
// unreachable server surfaces the server's own message rather than a generic connect
// timeout. Session creation counts: before a first success nothing proves the request
// valid. Later failures are ordinary reconnect material.
func (w *Watcher) fail(cause error) error {
	w.stateMu.Lock()
	fatal := !w.subscribed
	if fatal {
		w.err = cause
	}
	announce := !fatal && !w.warned
	w.stateMu.Unlock()

	if fatal {
		w.Stop()
		return cause
	}

	if announce {
		// Never park the dial loop on an undrained notice; warned is set only on a
		// delivered one, so a dropped notice is re-announced next attempt.
		select {
		case w.reconnectFailed <- cause:
			w.stateMu.Lock()
			w.warned = true
			w.stateMu.Unlock()
		default:
		}
	}

	return cause
}

func (w *Watcher) handleMessage(raw []byte) {
	// Copied because raw is handed to another goroutine through frames.
	frame := make([]byte, len(raw))
	copy(frame, raw)

	select {
	case w.frames <- frame:
	case <-w.done:
	}
}
