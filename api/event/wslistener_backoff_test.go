package event

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type listenerRetryError struct {
	error
	delay time.Duration
}

func (e listenerRetryError) RetryAfter() time.Duration { return e.delay }

func TestWSListenerFlapsBackOff(t *testing.T) {
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer ts.Close()
	var mu sync.Mutex
	var attempts []time.Time
	var w *wsListener
	w = newProvisionedWSListener(nil, func() (string, error) {
		mu.Lock()
		attempts = append(attempts, time.Now())
		count := len(attempts)
		mu.Unlock()
		if count == 5 {
			w.Stop()
			return "", errors.New("test finished")
		}
		return "ws" + strings.TrimPrefix(ts.URL, "http"), nil
	}, time.Second)
	w.reconnectBaseDelay = 80 * time.Millisecond
	w.handleFrame = func([]byte) {}
	w.Start()
	defer w.Stop()
	select {
	case <-w.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not finish reconnecting")
	}
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, attempts, 5)
	assert.GreaterOrEqual(t, attempts[3].Sub(attempts[2]), 2*w.reconnectBaseDelay)
	assert.GreaterOrEqual(t, attempts[4].Sub(attempts[3]), 4*w.reconnectBaseDelay)
}

func TestWSListenerConnectionLifetime(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		provisionDelay, hookDelay, readDelay time.Duration
		healthy                              bool
	}{
		{name: "immediate close"},
		{name: "slow provisioning", provisionDelay: 100 * time.Millisecond},
		{name: "slow subscription", hookDelay: 100 * time.Millisecond},
		{name: "stable connection", readDelay: 100 * time.Millisecond, healthy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.WriteMessage(websocket.TextMessage, []byte("frame"))
			}))
			defer ts.Close()
			w := newProvisionedWSListener(nil, func() (string, error) {
				time.Sleep(tc.provisionDelay)
				return "ws" + strings.TrimPrefix(ts.URL, "http"), nil
			}, time.Second)
			w.reconnectBaseDelay = 50 * time.Millisecond
			w.onConnected = func() error { time.Sleep(tc.hookDelay); return nil }
			w.handleFrame = func([]byte) { time.Sleep(tc.readDelay) }
			lifetime, retryAfter := w.connectAndListen()
			assert.Zero(t, retryAfter)
			if tc.healthy {
				assert.Greater(t, lifetime, w.reconnectBaseDelay)
			} else {
				assert.Less(t, lifetime, w.reconnectBaseDelay)
			}
		})
	}
}

func TestReconnectWait(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                         string
		delay, retryAfter, low, high time.Duration
	}{
		{"base jitter", time.Second, 0, 500 * time.Millisecond, time.Second},
		{"cap jitter", wsReconnectMaxDelay, 0, wsReconnectMaxDelay / 2, wsReconnectMaxDelay},
		{"server minimum", time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second},
		{"server above cap", wsReconnectMaxDelay, time.Minute, time.Minute, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 100 {
				wait := reconnectWait(tc.delay, tc.retryAfter)
				assert.GreaterOrEqual(t, wait, tc.low)
				assert.LessOrEqual(t, wait, tc.high)
			}
		})
	}
}

func TestWSListenerWaitsForRetryAfter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		w := newProvisionedWSListener(nil, func() (string, error) {
			calls.Add(1)
			return "", listenerRetryError{errors.New("throttled"), time.Minute}
		}, time.Second)
		w.handleFrame = func([]byte) {}
		w.Start()
		defer w.Stop()
		synctest.Wait()
		time.Sleep(time.Minute - time.Nanosecond)
		assert.Equal(t, int32(1), calls.Load())
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, int32(2), calls.Load())
	})
}

func TestWSListenerRetryAfterSources(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"provision", "handshake", "subscription"} {
		t.Run(source, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if source == "handshake" {
					w.Header().Set("Retry-After", "29")
					w.WriteHeader(429)
					return
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err == nil {
					defer func() { _ = conn.Close() }()
					_, _, _ = conn.ReadMessage()
				}
			}))
			defer ts.Close()
			retryErr := listenerRetryError{errors.New("throttled"), 29 * time.Second}
			w := newProvisionedWSListener(nil, func() (string, error) {
				if source == "provision" {
					return "", retryErr
				}
				return "ws" + strings.TrimPrefix(ts.URL, "http"), nil
			}, time.Second)
			w.onConnected = func() error { return retryErr }
			w.handleFrame = func([]byte) {}
			lifetime, retryAfter := w.connectAndListen()
			assert.Zero(t, lifetime)
			assert.Equal(t, 29*time.Second, retryAfter)
		})
	}
}
