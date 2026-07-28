package event

import (
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
