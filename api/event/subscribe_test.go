package event

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestSubscribeEvent_SendsExpectedPayload(t *testing.T) {
	tests := []struct {
		name      string
		eventType EventType
		targetID  string
	}{
		{name: "sudo", eventType: EventTypeSudo, targetID: "session-123"},
		{name: "command output", eventType: EventTypeCommandOutput, targetID: "command-uuid"},
		{name: "type the CLI does not know yet", eventType: "work_session", targetID: "ws-uuid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Contains(t, r.URL.Path, "events/subscriptions")

				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)

				var req EventSubscriptionRequest
				require.NoError(t, json.Unmarshal(body, &req))
				assert.Equal(t, "channel-456", req.Channel)
				assert.Equal(t, tt.eventType, req.EventType)
				assert.Equal(t, tt.targetID, req.TargetID)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":"sub-789"}`))
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{
				HTTPClient: ts.Client(),
				BaseURL:    ts.URL,
			}

			err := SubscribeEvent(ac, "channel-456", tt.eventType, tt.targetID)
			assert.NoError(t, err)
		})
	}
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

func TestSubscribeEvent_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	err := SubscribeEvent(ac, "channel-456", EventTypeSudo, "session-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to subscribe to sudo events: ")
}

func TestSubscribeEvent_NotFoundKeepsServerMessageLast(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail": "Not found."}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	err := SubscribeEvent(ac, "channel-456", EventTypeSudo, "session-123")
	require.Error(t, err)
	// websh's isNotFoundError matches a literal ": not found." suffix on the
	// lowercased message, so the wrap must keep the server's detail last.
	assert.True(t, strings.HasSuffix(strings.ToLower(err.Error()), ": not found."), "got %q", err.Error())
}
