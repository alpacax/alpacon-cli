package event

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateEventSession(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	assert.Equal(t, expected.ID, resp.ID)
	assert.Equal(t, expected.WebsocketURL, resp.WebsocketURL)
	assert.Equal(t, expected.ChannelID, resp.ChannelID)
}

func TestSubscribeEvent_SendsExpectedPayload(t *testing.T) {
	t.Parallel()
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
				if !assert.NoError(t, err) {
					return
				}

				var req EventSubscriptionRequest
				if !assert.NoError(t, json.Unmarshal(body, &req)) {
					return
				}
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

func TestSubscribeEvent_OmitsEmptyTarget(t *testing.T) {
	t.Parallel()
	type decoded struct {
		body map[string]any
		err  error
	}
	bodies := make(chan decoded, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		bodies <- decoded{body: body, err: err}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"sub-789"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	require.NoError(t, SubscribeEvent(ac, "channel-456", "notification", ""))

	got := <-bodies
	require.NoError(t, got.err)
	body := got.body
	_, present := body["target_id"]
	// The server validates target_id as a UUID, so an empty string would be a 400.
	assert.False(t, present, "empty target must be omitted from the request body")
	assert.Equal(t, "channel-456", body["channel"])
	assert.Equal(t, "notification", body["event_type"])
}

func TestCreateEventSession_ServerError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	resp, err := CreateEventSession(ac)
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestSubscribeEvent_ServerError(t *testing.T) {
	t.Parallel()
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

func TestSubscribeEvent_NotFoundKeepsTheStatusReachable(t *testing.T) {
	t.Parallel()
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
	// websh reads the status off this error to stay quiet on an older server, so the
	// wrap must keep it reachable.
	assert.Equal(t, http.StatusNotFound, utils.HTTPStatusCode(err))
}

func TestCloseEventSession_SendsADeleteForTheSession(t *testing.T) {
	t.Parallel()
	var method, uri string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, uri = r.Method, r.RequestURI
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	err := CloseEventSession(t.Context(), ac, "session-1")

	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/api/events/sessions/session-1/", uri)
}

// The id comes from a server response, so a slash in it must stay inside its own
// path segment instead of reaching another route.
func TestCloseEventSession_KeepsTheIDInOneSegment(t *testing.T) {
	t.Parallel()
	var uri string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri = r.RequestURI
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	err := CloseEventSession(t.Context(), ac, "../iam/users/-")

	require.NoError(t, err)
	assert.Equal(t, "/api/events/sessions/..%2Fiam%2Fusers%2F-/", uri)
}

// url.PathEscape keeps "." and "..", which a server or proxy may resolve to another
// route, so an id that is not one real segment sends nothing.
func TestCloseEventSession_RefusesAnIDThatIsNotOneSegment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, id string }{
		{name: "empty", id: ""},
		{name: "dot", id: "."},
		{name: "dot_dot", id: ".."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			err := CloseEventSession(t.Context(), ac, tc.id)

			require.Error(t, err)
			assert.Zero(t, requests.Load())
		})
	}
}

// A server that lacks the route or fails it still gets exactly one attempt: a
// session left open only reads as open.
func TestCloseSession_TriesAFailedCloseOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "not_found", status: http.StatusNotFound},
		{name: "method_not_allowed", status: http.StatusMethodNotAllowed},
		{name: "server_error", status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var deletes atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				deletes.Add(1)
				w.WriteHeader(tc.status)
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			closeSession(ac, "session-1", time.Second)

			assert.Equal(t, int32(1), deletes.Load())
		})
	}
}

func TestCloseSession_GivesUpAtItsDeadline(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second): // a request sent without the deadline fails the elapsed check, not the run
		}
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	start := time.Now()
	closeSession(ac, "session-1", 50*time.Millisecond)

	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestCloseSession_LeavesADebugLineWhenTheCloseFails(t *testing.T) {
	t.Setenv(utils.DebugEnvVar, "1")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		closeSession(ac, "session-1", time.Second)
	})

	assert.Contains(t, stderr, "could not close unopened event session session-1")
}

// Websh holds the terminal in raw mode, so a failed close must print nothing
// unless debug output was asked for.
func TestCloseSession_StaysQuietWithoutTheDebugSwitch(t *testing.T) {
	t.Setenv(utils.DebugEnvVar, "")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		closeSession(ac, "session-1", time.Second)
	})

	assert.Empty(t, stderr)
}
