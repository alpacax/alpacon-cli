package event

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSudoMFAEvent_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	payload := sudoMFAEvent{}
	payload.Payload.Type = "auth"
	payload.Payload.Query = "mfa_request"
	payload.Payload.SudoGrantID = "test-grant-id"
	payload.Payload.MfaURL = "https://auth.alpacon.io/mfa?token=abc"
	payload.Payload.Command = "sudo systemctl restart nginx"
	payload.Payload.SessionID = "test-session-id"

	msg, err := json.Marshal(payload)
	require.NoError(t, err)

	var parsed sudoMFAEvent
	err = json.Unmarshal(msg, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "auth", parsed.Payload.Type)
	assert.Equal(t, "mfa_request", parsed.Payload.Query)
	assert.Equal(t, "test-grant-id", parsed.Payload.SudoGrantID)
	assert.Equal(t, "https://auth.alpacon.io/mfa?token=abc", parsed.Payload.MfaURL)
}

func TestSudoListener_HandleMessage_IgnoresNonMFA(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "wrong type",
			payload: `{"payload":{"type":"notification","query":"mfa_request"}}`,
		},
		{
			name:    "wrong query",
			payload: `{"payload":{"type":"auth","query":"other"}}`,
		},
		{
			name:    "empty payload",
			payload: `{}`,
		},
		{
			name:    "invalid json",
			payload: `not json`,
		},
	}

	sl := NewSudoListener(nil, "", "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sl.handleMessage([]byte(tt.payload))
		})
	}
}

func TestSudoListener_HandleSudoMFA_DropsRequestWhenClientIsNil(t *testing.T) {
	t.Parallel()
	sl := NewSudoListener(nil, "", "")

	var event sudoMFAEvent
	event.Payload.Type = "auth"
	event.Payload.Query = "mfa_request"
	event.Payload.SudoGrantID = "test-grant-id"

	assert.NotPanics(t, func() { sl.handleSudoMFA(event) })
}

func TestSudoListener_StopClosesConnection(t *testing.T) {
	t.Parallel()
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.Start()

	require.True(t, sl.WaitConnected(3*time.Second), "the listener must connect before Stop has a connection to close")

	sl.Stop()

	select {
	case <-sl.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for listener goroutine to exit")
	}

	sl.mu.Lock()
	assert.Nil(t, sl.conn, "connection should be nil after stop")
	sl.mu.Unlock()
}

func TestSudoListener_ConnectAndListen_ExitsOnDisconnect(t *testing.T) {
	t.Parallel()
	clientRead := make(chan struct{})

	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		defer func() { _ = conn.Close() }()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"payload":{"type":"info","query":"status"}}`))
		<-clientRead
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")

	// Local signal, so the test does not close the skeleton's stopped channel
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_ = sl.connectAndListen()
	}()

	require.Eventually(t, func() bool {
		sl.mu.Lock()
		defer sl.mu.Unlock()
		return sl.conn != nil
	}, 2*time.Second, 10*time.Millisecond)

	close(clientRead) // server closes → client read loop exits

	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for connectAndListen to return")
	}
}

func newTestSudoListener(ts *httptest.Server) *SudoListener {
	return NewSudoListener(&client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}, "", "")
}

func TestSudoListener_VerifySudoGrant_Success(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/api/sudo/grants/grant-123/verify/")

		// Verify carries no payload — the server resolves MFA from the
		// MFACompletion record, so the body must stay empty.
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, "{}", string(body))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	sl := newTestSudoListener(ts)

	err := sl.verifySudoGrant("grant-123")
	assert.NoError(t, err)
}

func TestSudoListener_VerifySudoGrant_ServerError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	sl := newTestSudoListener(ts)

	err := sl.verifySudoGrant("grant-123")
	assert.Error(t, err)
}

func TestSudoListener_PollMFACompletion_Timeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sl := NewSudoListener(nil, "", "")

		start := time.Now()
		go func() {
			time.Sleep(100 * time.Millisecond)
			sl.Stop()
		}()

		result := sl.pollMFACompletion()
		elapsed := time.Since(start)

		assert.False(t, result, "should return false when stopped")
		assert.Less(t, elapsed, 2*time.Second, "should exit quickly when stopped")
	})
}

func TestSudoListener_CreatesANewSessionOnReconnect(t *testing.T) {
	t.Parallel()
	ts, sessions, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			// The token is single-use, so recovering means a whole new session.
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.reconnectBaseDelay = testReconnectBaseDelay
	sl.Start()
	defer sl.Stop()

	assert.Eventually(t, func() bool {
		return sessions.Load() >= 2
	}, 5*time.Second, 10*time.Millisecond, "each attempt must create its own event session")
}

func TestSudoListener_ResubscribesOnReconnect(t *testing.T) {
	t.Parallel()
	ts, _, subscribes := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.reconnectBaseDelay = testReconnectBaseDelay
	sl.Start()
	defer sl.Stop()

	assert.Eventually(t, func() bool {
		return subscribes.Load() >= 2
	}, 5*time.Second, 10*time.Millisecond, "a reconnect must re-issue the sudo subscription")
}

func TestSudoListener_FirstSubscribeFailureStopsAndSurfaces(t *testing.T) {
	t.Parallel()
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysRejected, func(conn *websocket.Conn, _ int32) {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.Start()
	defer sl.Stop()

	assert.False(t, sl.WaitConnected(3*time.Second), "a rejected subscription must not report connected")

	err := sl.Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to subscribe to sudo events")
}

func TestSudoListener_SessionCreateRetryableStatusIsRetried(t *testing.T) {
	t.Parallel()
	failFirst := func(attempt int32) int {
		if attempt == 1 {
			return http.StatusServiceUnavailable
		}
		return http.StatusCreated
	}

	ts, _, _ := newWatcherTestServer(t, failFirst, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.reconnectBaseDelay = testReconnectBaseDelay
	sl.Start()
	defer sl.Stop()

	assert.True(t, sl.WaitConnected(3*time.Second), "a 503 before the first subscribe is not fatal; the next attempt must connect")
	assert.NoError(t, sl.Err())
}

func TestSudoListener_AnnouncesOutageOncePerOutage(t *testing.T) {
	// Subscribed once, then every later dial fails: exactly one outage.
	upgradeFirstOnly := func(attempt int32) bool { return attempt == 1 }

	ts, sessions, _ := newWatcherTestServer(t, alwaysCreated, upgradeFirstOnly, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.Close()
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		sl := NewSudoListener(ac, "my-server", "session-1")
		sl.reconnectBaseDelay = testReconnectBaseDelay
		sl.Start()
		defer sl.Stop()

		require.True(t, sl.WaitConnected(3*time.Second), "the first dial must subscribe before an outage counts")

		// A third session means at least two dials failed after the first connect.
		require.Eventually(t, func() bool {
			return sessions.Load() >= 3
		}, 3*time.Second, 10*time.Millisecond)
	})

	assert.Equal(t, 1, strings.Count(stderr, sudoOutageWarning),
		"one outage must produce exactly one warning, not one per retry")
}

func TestSudoListener_StaysQuietBeforeFirstSubscribe(t *testing.T) {
	// It never subscribes, so websh's WaitConnected failure is the only thing that speaks.
	neverUpgrade := func(int32) bool { return false }

	ts, _, _ := newWatcherTestServer(t, alwaysCreated, neverUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		sl := NewSudoListener(ac, "my-server", "session-1")
		sl.reconnectBaseDelay = testReconnectBaseDelay
		sl.Start()
		defer sl.Stop()

		assert.False(t, sl.WaitConnected(300*time.Millisecond), "a listener that never upgrades must not report a connection")
	})

	assert.NotContains(t, stderr, sudoOutageWarning)
}

func TestSudoListener_AnnouncesOutageWhenSessionCreateFails(t *testing.T) {
	// This outage never reaches the dial, so onDialFailed is not what announces it.
	createThenFail := func(attempt int32) int {
		if attempt == 1 {
			return http.StatusCreated
		}
		return http.StatusInternalServerError
	}
	upgradeFirstOnly := func(attempt int32) bool { return attempt == 1 }

	ts, sessions, _ := newWatcherTestServer(t, createThenFail, upgradeFirstOnly, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.Close()
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		sl := NewSudoListener(ac, "my-server", "session-1")
		sl.reconnectBaseDelay = testReconnectBaseDelay
		sl.Start()
		defer sl.Stop()

		require.True(t, sl.WaitConnected(3*time.Second), "the first dial must subscribe before an outage counts")

		require.Eventually(t, func() bool {
			return sessions.Load() >= 3
		}, 3*time.Second, 10*time.Millisecond)
	})

	assert.Equal(t, 1, strings.Count(stderr, sudoOutageWarning),
		"a session create that keeps failing is an outage and must be announced once")
}

func TestSudoListener_AnnouncesOutageWhenResubscribeFails(t *testing.T) {
	// The dial succeeds every time; only the resubscribe fails, so onDialFailed never fires.
	createThenFail := func(attempt int32) int {
		if attempt == 1 {
			return http.StatusCreated
		}
		return http.StatusInternalServerError
	}

	ts, _, subscribes := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, createThenFail, func(conn *websocket.Conn, _ int32) {
		_ = conn.Close()
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		sl := NewSudoListener(ac, "my-server", "session-1")
		sl.reconnectBaseDelay = testReconnectBaseDelay
		sl.Start()
		defer sl.Stop()

		require.True(t, sl.WaitConnected(3*time.Second), "the first dial must subscribe before an outage counts")

		require.Eventually(t, func() bool {
			return subscribes.Load() >= 3
		}, 3*time.Second, 10*time.Millisecond)
	})

	assert.Equal(t, 1, strings.Count(stderr, sudoOutageWarning),
		"a resubscribe that keeps failing is an outage and must be announced once")
}

func TestSudoListener_AnnouncesTheNextOutageAfterRecovering(t *testing.T) {
	// Outage, recovery, outage: the recovery must clear the warned flag.
	upgradeFirstAndThird := func(attempt int32) bool { return attempt == 1 || attempt == 3 }

	ts, sessions, _ := newWatcherTestServer(t, alwaysCreated, upgradeFirstAndThird, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.Close()
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		sl := NewSudoListener(ac, "my-server", "session-1")
		sl.reconnectBaseDelay = testReconnectBaseDelay
		sl.Start()
		defer sl.Stop()

		require.True(t, sl.WaitConnected(3*time.Second), "the first dial must connect and subscribe")

		// Six sessions means the fifth dial already failed, so the second outage has spoken.
		require.Eventually(t, func() bool {
			return sessions.Load() >= 6
		}, 5*time.Second, 10*time.Millisecond)
	})

	assert.Equal(t, 2, strings.Count(stderr, sudoOutageWarning),
		"a recovered channel makes the next outage a new one, worth announcing again")
}

func TestSudoListener_FirstSessionFailureStopsAndSurfaces(t *testing.T) {
	t.Parallel()
	ts, _, _ := newWatcherTestServer(t, alwaysRejected, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.reconnectBaseDelay = testReconnectBaseDelay
	sl.Start()
	defer sl.Stop()

	// It must end the wait rather than retry, and websh needs the server's reason.
	assert.False(t, sl.WaitConnected(3*time.Second), "a rejected session must not report a connection")

	err := sl.Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create event session")
}

func TestSudoListener_RefreshesTheAccessTokenOnUnauthorized(t *testing.T) {
	// The token expires only after the listener is subscribed, where nothing is fatal.
	createThenUnauthorized := func(attempt int32) int {
		if attempt == 1 {
			return http.StatusCreated
		}
		return http.StatusUnauthorized
	}

	ts, sessions, _ := newWatcherTestServer(t, createThenUnauthorized, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.Close()
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	var refreshes atomic.Int32
	testutil.CaptureOutput(t, func() {
		sl := NewSudoListener(ac, "my-server", "session-1")
		sl.reconnectBaseDelay = testReconnectBaseDelay
		sl.refreshToken = func() error {
			refreshes.Add(1)
			return nil
		}
		sl.Start()
		defer sl.Stop()

		require.True(t, sl.WaitConnected(3*time.Second), "the first dial must subscribe before a 401 counts as post-subscribe")

		require.Eventually(t, func() bool {
			return refreshes.Load() >= 1
		}, 3*time.Second, 10*time.Millisecond, "a 401 after the first subscribe must renew the token instead of re-presenting it")
	})

	assert.Greater(t, sessions.Load(), int32(1))
}

func TestSudoListener_LeavesTheTokenAloneOnOtherStatuses(t *testing.T) {
	createThenServerError := func(attempt int32) int {
		if attempt == 1 {
			return http.StatusCreated
		}
		return http.StatusInternalServerError
	}

	ts, sessions, _ := newWatcherTestServer(t, createThenServerError, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.Close()
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	var refreshes atomic.Int32
	testutil.CaptureOutput(t, func() {
		sl := NewSudoListener(ac, "my-server", "session-1")
		sl.reconnectBaseDelay = testReconnectBaseDelay
		sl.refreshToken = func() error {
			refreshes.Add(1)
			return nil
		}
		sl.Start()
		defer sl.Stop()

		require.True(t, sl.WaitConnected(3*time.Second), "the first dial must subscribe")

		require.Eventually(t, func() bool {
			return sessions.Load() >= 3
		}, 3*time.Second, 10*time.Millisecond)
	})

	assert.Zero(t, refreshes.Load(), "only a 401 says the token is the problem")
}

func TestSudoListener_StaysQuietWhenAFailureLandsAfterStop(t *testing.T) {
	sl := NewSudoListener(nil, "my-server", "session-1")
	sl.stateMu.Lock()
	sl.subscribed = true
	sl.stateMu.Unlock()

	sl.Stop()

	_, stderr := testutil.CaptureOutput(t, func() {
		// Stop cancels no in-flight dial, so its failure still reaches fail afterwards.
		_ = sl.fail(errors.New("dial failed after the stop"))
	})

	assert.NotContains(t, stderr, sudoOutageWarning)
}

func TestSudoListener_SurfacesANonFatalCauseAfterTheWaitEnds(t *testing.T) {
	t.Parallel()
	alwaysServerError := func(int32) int { return http.StatusInternalServerError }

	ts, _, _ := newWatcherTestServer(t, alwaysServerError, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.reconnectBaseDelay = testReconnectBaseDelay
	sl.Start()
	defer sl.Stop()

	// A 500 is retried, so the wait runs out; websh still has to name the reason.
	assert.False(t, sl.WaitConnected(500*time.Millisecond), "a listener that never creates a session must not report connected")

	err := sl.Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create event session")
}

func TestSudoListener_TriesOneRefreshBeforeGivingUpOnAnUnauthorizedFirstSession(t *testing.T) {
	t.Parallel()
	alwaysUnauthorized := func(int32) int { return http.StatusUnauthorized }

	ts, sessions, _ := newWatcherTestServer(t, alwaysUnauthorized, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	sl := NewSudoListener(ac, "my-server", "session-1")
	sl.reconnectBaseDelay = testReconnectBaseDelay

	var refreshes atomic.Int32
	sl.refreshToken = func() error {
		refreshes.Add(1)
		return nil
	}
	sl.Start()
	defer sl.Stop()

	select {
	case <-sl.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("a 401 answering a freshly renewed token must stop the listener")
	}

	assert.Equal(t, int32(1), refreshes.Load(), "the refusal is only worth one refresh")
	assert.Equal(t, int32(2), sessions.Load(), "that refresh buys exactly one more attempt")
	assert.Error(t, sl.Err(), "websh still needs the reason it gave up")
}
