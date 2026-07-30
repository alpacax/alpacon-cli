package event

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWatcherTestServer serves the session, subscription, and WebSocket endpoints on one
// httptest server so a Watcher can run end to end. Each hook receives its own 1-based
// attempt count, in the order the runtime reaches them.
func newWatcherTestServer(t *testing.T, sessionStatus func(attempt int32) int, shouldUpgrade func(attempt int32) bool, subscribeStatus func(attempt int32) int, wsHandler func(conn *websocket.Conn, n int32)) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()

	var sessions, conns, subscribes atomic.Int32
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()

	ts := httptest.NewServer(mux)

	mux.HandleFunc(eventSessionsURL, func(w http.ResponseWriter, r *http.Request) {
		n := sessions.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if status := sessionStatus(n); status >= 400 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":"Bad gateway."}`))
			return
		}
		_ = json.NewEncoder(w).Encode(EventSessionResponse{
			ID:           "session",
			WebsocketURL: "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/",
			ChannelID:    fmt.Sprintf("channel-%d", n),
		})
	})

	mux.HandleFunc(eventSubscriptionsURL, func(w http.ResponseWriter, r *http.Request) {
		status := subscribeStatus(subscribes.Add(1))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = w.Write([]byte(`{"detail":"Invalid input."}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"sub"}`))
	})

	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		n := conns.Add(1)
		if !shouldUpgrade(n) {
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		wsHandler(conn, n)
	})

	t.Cleanup(ts.Close)
	return ts, &sessions, &subscribes
}

func alwaysCreated(int32) int { return http.StatusCreated }

func alwaysUpgrade(int32) bool { return true }

func alwaysRejected(int32) int { return http.StatusBadRequest }

func TestWatcher_ForwardsFramesVerbatim(t *testing.T) {
	frame := `{"event_type":"work_session","payload":{"category":"status","sub_type":"approved","unknown":1}}`

	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(frame))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWatcher(ac, "work_session", "ws-uuid")
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(3*time.Second))
	require.NoError(t, w.Err())

	select {
	case got := <-w.Frames():
		assert.JSONEq(t, frame, string(got))
	case <-time.After(3 * time.Second):
		t.Fatal("no frame received")
	}
}

func TestWatcher_CreatesANewSessionOnReconnect(t *testing.T) {
	ts, sessions, subscribes := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			// The token is single-use, so recovering means a whole new session.
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWatcher(ac, "work_session", "ws-uuid")
	w.reconnectBaseDelay = testReconnectBaseDelay
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(3*time.Second))

	select {
	case <-w.Reconnected():
	case <-time.After(5 * time.Second):
		t.Fatalf("expected a reconnect, sessions=%d subscribes=%d", sessions.Load(), subscribes.Load())
	}

	assert.GreaterOrEqual(t, sessions.Load(), int32(2), "each attempt must create its own event session")
}

func TestWatcher_FirstSubscribeFailureStopsAndSurfaces(t *testing.T) {
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysRejected, func(conn *websocket.Conn, _ int32) {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWatcher(ac, "work_session", "not-a-uuid")
	w.Start()
	defer w.Stop()

	assert.False(t, w.WaitConnected(3*time.Second), "a rejected subscription must not report connected")

	err := w.Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to subscribe to work_session events")
}

func TestWatcher_FailureAfterFirstSuccessIsNonFatal(t *testing.T) {
	firstOnly := func(attempt int32) int {
		if attempt == 1 {
			return http.StatusCreated
		}
		return http.StatusBadRequest
	}

	ts, _, subscribes := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, firstOnly, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			// Dropped only after its subscribe succeeded, so the reconnect is the
			// attempt that gets rejected.
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWatcher(ac, "work_session", "ws-uuid")
	w.reconnectBaseDelay = testReconnectBaseDelay
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(3*time.Second))
	require.NoError(t, w.Err())

	require.Eventually(t, func() bool {
		return subscribes.Load() >= 2
	}, 5*time.Second, 10*time.Millisecond, "expected a rejected re-subscribe")

	assert.NoError(t, w.Err())
	select {
	case <-w.done:
		t.Fatal("watcher stopped after a post-success subscribe failure")
	default:
	}

	select {
	case failErr := <-w.ReconnectFailed():
		assert.ErrorContains(t, failErr, "failed to subscribe to work_session events")
	case <-time.After(5 * time.Second):
		t.Fatal("expected a ReconnectFailed notice after a post-success failure")
	}

	require.Eventually(t, func() bool {
		return subscribes.Load() >= 3
	}, 5*time.Second, 10*time.Millisecond, "expected the listener to keep retrying")

	assert.NoError(t, w.Err())
	select {
	case <-w.done:
		t.Fatal("watcher stopped after a post-success subscribe failure")
	default:
	}
}

func TestWatcher_DialFailureAfterFirstSuccessIsAnnounced(t *testing.T) {
	// A refused handshake is a real dial error, which reaches neither provision nor
	// subscribe.
	firstConnOnly := func(attempt int32) bool { return attempt == 1 }

	ts, _, _ := newWatcherTestServer(t, alwaysCreated, firstConnOnly, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.Close()
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWatcher(ac, "work_session", "ws-uuid")
	w.reconnectBaseDelay = testReconnectBaseDelay
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(3*time.Second))

	select {
	case failErr := <-w.ReconnectFailed():
		assert.Error(t, failErr)
	case <-time.After(5 * time.Second):
		t.Fatal("a dial failure after a first success must be announced")
	}

	assert.NoError(t, w.Err(), "a dial failure after a first success must not be fatal")
}

func TestWatcher_StaleFailureNoticeIsDroppedOnRecovery(t *testing.T) {
	secondOnly := func(attempt int32) int {
		if attempt == 2 {
			return http.StatusBadRequest
		}
		return http.StatusCreated
	}

	ts, _, subscribes := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, secondOnly, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWatcher(ac, "work_session", "ws-uuid")
	w.reconnectBaseDelay = testReconnectBaseDelay
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(3*time.Second))

	// ReconnectFailed is deliberately never drained: attempt 2 queues a notice that
	// attempt 3's recovery must discard.
	require.Eventually(t, func() bool {
		return subscribes.Load() >= 3
	}, 5*time.Second, 10*time.Millisecond, "expected a recovery after the rejected re-subscribe")

	select {
	case <-w.Reconnected():
	case <-time.After(3 * time.Second):
		t.Fatal("expected a reconnect notice after recovery")
	}

	select {
	case failErr := <-w.ReconnectFailed():
		t.Fatalf("a resolved outage must not stay queued, got %v", failErr)
	default:
	}
}

func TestWatcher_MalformedWebsocketURLStopsAndSurfaces(t *testing.T) {
	const token = "hunter2"
	// A DEL byte makes url.Parse fail; left to the dialer that surfaces as a *url.Error
	// quoting the whole URL, token included.
	badURL := "ws://127.0.0.1/ws/session/channel/" + token + "/\x7f"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EventSessionResponse{
			ID:           "session",
			WebsocketURL: badURL,
			ChannelID:    "channel",
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWatcher(ac, "work_session", "ws-uuid")
	w.Start()
	defer w.Stop()

	assert.False(t, w.WaitConnected(2*time.Second))

	err := w.Err()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), token, "a malformed-url error must not echo the channel token")
	assert.Contains(t, err.Error(), "malformed event channel URL")
}

func TestWatcher_SessionCreateRejectionStopsAndSurfaces(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Invalid token."}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWatcher(ac, "work_session", "ws-uuid")
	w.Start()
	defer w.Stop()

	assert.False(t, w.WaitConnected(2*time.Second))
	require.Error(t, w.Err())
	assert.Contains(t, w.Err().Error(), "failed to create event session")
}

func TestWatcher_SessionCreateRetryableStatusIsRetried(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"server error", http.StatusBadGateway},
		// Both are 4xx that ask to be retried, so the fatal band has to skip them.
		{"request timeout", http.StatusRequestTimeout},
		{"too many requests", http.StatusTooManyRequests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failFirst := func(attempt int32) int {
				if attempt == 1 {
					return tt.status
				}
				return http.StatusCreated
			}

			ts, sessions, _ := newWatcherTestServer(t, failFirst, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			})

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			w := NewWatcher(ac, "work_session", "ws-uuid")
			w.reconnectBaseDelay = testReconnectBaseDelay
			w.Start()
			defer w.Stop()

			require.True(t, w.WaitConnected(3*time.Second), "a transient session-create failure must be absorbed, not fatal")
			assert.NoError(t, w.Err())
			assert.GreaterOrEqual(t, sessions.Load(), int32(2), "the failed attempt must be retried with a new session")
		})
	}
}
