package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestClient(baseURL string) *AlpaconClient {
	return &AlpaconClient{
		HTTPClient: &http.Client{},
		BaseURL:    baseURL,
		Token:      "test-token",
	}
}

func TestSendRequest_401ReturnsAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "invalid token"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.ErrorContains(t, err, "authentication failed")
}

func TestSendRequest_403ReturnsForbiddenError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "forbidden"}`))
	}))
	defer ts.Close()

	ac := newTestClient(ts.URL)
	_, err := ac.SendGetRequest("/api/test/")
	assert.ErrorContains(t, err, "permission denied")
}
