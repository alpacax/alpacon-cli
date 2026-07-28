package event

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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
