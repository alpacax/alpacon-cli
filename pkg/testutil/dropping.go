package testutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// NewDroppingServer returns a server that closes every connection without answering, so a
// request to it fails with no HTTP status, as one to an unreachable host does. A closed
// httptest.Server's URL cannot stand in for that: the port it frees can be handed to another
// server the same test binary starts, and the request then gets an answer.
func NewDroppingServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(ts.Close)
	return ts
}
