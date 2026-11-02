package event

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
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

func TestCloseEventSession_RefusesAnIDThatIsNotOneSegment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, id string }{
		{name: "empty", id: ""},
		{name: "dot", id: "."},
		{name: "dot_dot", id: ".."},
		{name: "parent_traversal", id: "../../etc/test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			err := CloseEventSession(t.Context(), ac, tc.id)

			require.ErrorIs(t, err, utils.ErrInvalidID)
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
			synctest.Test(t, func(t *testing.T) {
				var deletes atomic.Int32
				ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					deletes.Add(1)
					w.WriteHeader(tc.status)
				}))
				ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

				start := time.Now()
				closeSession(ac, "session-1", time.Second)
				synctest.Wait()

				assert.Equal(t, int32(1), deletes.Load())
				assert.Zero(t, time.Since(start), "a failed close must not be retried after a pause")
			})
		})
	}
}

func TestCloseSession_GivesUpAtItsDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		arrived := make(chan struct{}, 1)
		ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			arrived <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second): // a request sent without the deadline fails the elapsed check, not the run
			}
		}))
		ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

		start := time.Now()
		closeSession(ac, "session-1", 50*time.Millisecond)

		assert.Equal(t, 50*time.Millisecond, time.Since(start))
		select {
		case <-arrived:
		default:
			t.Error("the close never reached the server, so the deadline was not what ended it")
		}
	})
}

// Websh holds the terminal in raw mode, so a failed close must print nothing
// unless debug output was asked for.
func TestCloseSession_PrintsAFailureOnlyWithTheDebugSwitch(t *testing.T) {
	for _, tc := range []struct {
		name, debug, want string
	}{
		{name: "debug_on", debug: "1", want: "could not close unopened event session session-1"},
		{name: "debug_off", debug: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(utils.DebugEnvVar, tc.debug)

			// The capture pipe is a real file, so it stays outside the bubble.
			_, stderr := testutil.CaptureOutput(t, func() {
				synctest.Test(t, func(t *testing.T) {
					ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusInternalServerError)
					}))
					ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

					closeSession(ac, "session-1", time.Second)
				})
			})

			if tc.want == "" {
				assert.Empty(t, stderr)
				return
			}
			assert.Contains(t, stderr, tc.want)
		})
	}
}
