package event

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestCreateEventSession(t *testing.T) {
	expected := EventSessionResponse{
		ID:           "session-123",
		WebsocketURL: "ws://localhost/ws/event/session-123/channel-456/token/",
		ChannelID:    "channel-456",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "events/sessions")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	resp, err := CreateEventSession(ac)
	assert.NoError(t, err)
	assert.Equal(t, expected.ID, resp.ID)
	assert.Equal(t, expected.WebsocketURL, resp.WebsocketURL)
	assert.Equal(t, expected.ChannelID, resp.ChannelID)
}

func TestSubscribeSudoEvent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "events/subscriptions")

		var req EventSubscriptionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "channel-456", req.Channel)
		assert.Equal(t, "sudo", req.EventType)
		assert.Equal(t, "session-123", req.TargetID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"sub-789"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	err := SubscribeSudoEvent(ac, "channel-456", "session-123")
	assert.NoError(t, err)
}

func TestCreateEventSession_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	resp, err := CreateEventSession(ac)
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSubscribeSudoEvent_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	err := SubscribeSudoEvent(ac, "channel-456", "session-123")
	assert.Error(t, err)
}
