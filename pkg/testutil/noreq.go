package testutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// NewNoRequestServer returns a server that fails the test on any request, for asserting a call
// was refused before anything was sent.
func NewNoRequestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	return ts
}
