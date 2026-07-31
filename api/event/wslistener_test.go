package event

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testReconnectBaseDelay keeps reconnect assertions off the production 1s backoff.
const testReconnectBaseDelay = 10 * time.Millisecond

func TestWSListener_StartPanicsWithoutHandleFrame(t *testing.T) {
	w := newWSListener(nil, "", 0)

	assert.PanicsWithValue(t, "event: wsListener.handleFrame must be assigned before Start", func() {
		w.Start()
	})
}

func TestWSListener_StopIsIdempotent(t *testing.T) {
	w := newWSListener(nil, "", 0)

	w.Stop()
	w.Stop()
	w.Stop()
}

func TestWSListener_WaitConnected_Success(t *testing.T) {
	w := newWSListener(nil, "", 0)

	// Simulate connection after short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(w.connected)
	}()

	result := w.WaitConnected(2 * time.Second)
	assert.True(t, result, "should return true when connected")
}

func TestWSListener_WaitConnected_Timeout(t *testing.T) {
	w := newWSListener(nil, "", 0)

	start := time.Now()
	result := w.WaitConnected(100 * time.Millisecond)
	elapsed := time.Since(start)

	assert.False(t, result, "should return false on timeout")
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.Less(t, elapsed, 1*time.Second)
}

func TestWSListener_NextReconnectDelay(t *testing.T) {
	tests := []struct {
		name  string
		delay time.Duration
		want  time.Duration
	}{
		{"base doubles", wsReconnectBaseDelay, 2 * time.Second},
		{"below cap doubles", 8 * time.Second, 16 * time.Second},
		{"overshoot clamps to cap", 16 * time.Second, wsReconnectMaxDelay},
		{"cap stays at cap", wsReconnectMaxDelay, wsReconnectMaxDelay},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nextReconnectDelay(tt.delay))
		})
	}
}

func TestWSListener_ConnectAndListen_ReturnsFalseOnFailedHandshake(t *testing.T) {
	// Responds 200 instead of upgrading, so Dial fails with ErrBadHandshake.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	w := newWSListener(nil, "ws"+strings.TrimPrefix(server.URL, "http"), time.Second)
	w.handleFrame = func([]byte) {}

	assert.False(t, w.connectAndListen(), "failed handshake should not count as connected")
	assert.False(t, w.WaitConnected(0), "connected must stay open after a failed dial")
}

func TestWSListener_ListenLoop_DoesNotDialWhenAlreadyStopped(t *testing.T) {
	var dialed atomic.Int32

	// Counted at handler entry so the increment happens-before Dial returns.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialed.Add(1)
	}))
	defer server.Close()

	w := newWSListener(nil, "ws"+strings.TrimPrefix(server.URL, "http"), time.Second)
	w.handleFrame = func([]byte) {}
	w.Stop()

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		w.listenLoop()
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("listenLoop should return immediately when done is already closed")
	}

	assert.Zero(t, dialed.Load(), "listenLoop should not dial after Stop")
}

func TestWSListener_WaitConnected_Shutdown(t *testing.T) {
	w := newWSListener(nil, "", 0)

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(w.done)
	}()

	start := time.Now()
	result := w.WaitConnected(5 * time.Second)
	elapsed := time.Since(start)

	assert.False(t, result, "should return false when done is closed")
	assert.Less(t, elapsed, 1*time.Second, "should exit quickly on shutdown")
}

func TestWSListener_ProvisionCalledPerDialAttempt(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var conns atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if conns.Add(1) == 1 {
			// Drop the first connection so the listener has to dial again.
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	var provisions atomic.Int32
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	w := newProvisionedWSListener(nil, func() (string, error) {
		provisions.Add(1)
		return wsURL, nil
	}, time.Second)
	w.handleFrame = func([]byte) {}
	w.reconnectBaseDelay = testReconnectBaseDelay
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(2*time.Second))

	// The listener reconnects on its own; wait for the second dial.
	deadline := time.After(5 * time.Second)
	for conns.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("expected a reconnect, conns=%d provisions=%d", conns.Load(), provisions.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}

	assert.GreaterOrEqual(t, provisions.Load(), int32(2), "provision must run once per dial attempt")
}

func TestWSListener_ProvisionErrorIsAFailedAttempt(t *testing.T) {
	var provisions atomic.Int32

	w := newProvisionedWSListener(nil, func() (string, error) {
		provisions.Add(1)
		return "", errors.New("provision failed")
	}, time.Second)
	w.handleFrame = func([]byte) {}

	assert.False(t, w.connectAndListen(), "a provision error must not count as connected")
	assert.Equal(t, int32(1), provisions.Load())
	assert.False(t, w.WaitConnected(0), "connected must stay open when provisioning fails")
}

func TestWSListener_OnConnectedRunsBeforeConnectedCloses(t *testing.T) {
	upgrader := websocket.Upgrader{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	// Atomics because the hook runs on the listener goroutine.
	var hookRan, openDuringHook atomic.Bool

	w := newProvisionedWSListener(nil, func() (string, error) {
		return "ws" + strings.TrimPrefix(ts.URL, "http"), nil
	}, time.Second)
	w.handleFrame = func([]byte) {}
	w.onConnected = func() error {
		hookRan.Store(true)
		openDuringHook.Store(!w.WaitConnected(0))
		return nil
	}
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(2*time.Second))
	assert.True(t, hookRan.Load(), "onConnected must run on a successful dial")
	assert.True(t, openDuringHook.Load(), "connected must not close before onConnected returns")
}

func TestWSListener_OnConnectedErrorIsAFailedAttempt(t *testing.T) {
	upgrader := websocket.Upgrader{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	w := newProvisionedWSListener(nil, func() (string, error) {
		return "ws" + strings.TrimPrefix(ts.URL, "http"), nil
	}, time.Second)
	w.handleFrame = func([]byte) {}
	w.onConnected = func() error { return errors.New("subscribe rejected") }

	assert.False(t, w.connectAndListen(), "an onConnected error must not count as connected")
	assert.False(t, w.WaitConnected(0), "connected must stay open when onConnected fails")
}
