package testutil

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// NewNoRequestServer returns a server that fails the test on any request, and the count of
// requests it saw, for asserting a call was refused before anything was sent.
func NewNoRequestServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}
