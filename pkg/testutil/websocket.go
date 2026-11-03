package testutil

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// handlerExitWait bounds how long cleanup waits for a handler blocked on something other than its connection.
const handlerExitWait = 10 * time.Second

// NewWebSocketServer is httptest.NewServer for a WebSocket handler: at the end of tb it also closes
// the hijacked connections Close leaves open and waits for every handler to return.
func NewWebSocketServer(tb testing.TB, h http.Handler) *httptest.Server {
	tb.Helper()
	var (
		handlers sync.WaitGroup
		mu       sync.Mutex
		hijacked []net.Conn
	)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		h.ServeHTTP(w, r)
	}))
	ts.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateHijacked {
			mu.Lock()
			hijacked = append(hijacked, conn)
			mu.Unlock()
		}
	}
	ts.Start()
	tb.Cleanup(func() {
		ts.Close() // waits for every request but the hijacked ones, so no handler starts after it
		mu.Lock()
		for _, conn := range hijacked {
			_ = conn.Close()
		}
		mu.Unlock()
		done := make(chan struct{})
		go func() {
			handlers.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(handlerExitWait):
			tb.Errorf("a WebSocket handler was still running %s after its connection closed", handlerExitWait)
		}
	})
	return ts
}
