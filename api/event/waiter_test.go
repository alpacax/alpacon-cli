package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testWaitTimeout  = 3 * time.Second
	testShortTimeout = 100 * time.Millisecond
)

// holdOpen keeps the connection alive so the waiter stays in its select loop.
func holdOpen(conn *websocket.Conn, _ int32) {
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func newTestWaiter(t *testing.T, ts *httptest.Server, opts WaitOptions) *Waiter {
	t.Helper()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := NewWaiter(ac, "work_session", "ws-uuid", opts)
	w.reconnectBaseDelay = testReconnectBaseDelay
	return w
}

func TestWaiter_CatchUpRunsOnlyAfterTheSubscriptionStands(t *testing.T) {
	var subscribes atomic.Int32
	var catchUpSawSubscribes atomic.Int32

	ts, _, subs := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, func(n int32) int {
		subscribes.Store(n)
		return http.StatusCreated
	}, holdOpen)
	_ = subs

	w := newTestWaiter(t, ts, WaitOptions{
		CatchUp: func() (string, error) {
			catchUpSawSubscribes.Store(subscribes.Load())
			return "pending", nil
		},
		OK:      []string{"approved"},
		Fail:    []string{"rejected"},
		Timeout: testShortTimeout,
	})

	_, outcome, err := w.Wait()

	require.NoError(t, err)
	assert.Equal(t, OutcomeTimeout, outcome, "pending must not end the wait")
	// If catch-up ran before subscribing, an event published in between would be lost.
	assert.GreaterOrEqual(t, catchUpSawSubscribes.Load(), int32(1), "catch-up must run after the subscription stands")
}

func TestWaiter_CatchUpDecidesWithoutAnEvent(t *testing.T) {
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, holdOpen)

	w := newTestWaiter(t, ts, WaitOptions{
		CatchUp: func() (string, error) { return "approved", nil },
		OK:      []string{"approved"},
		Fail:    []string{"rejected"},
		Timeout: testWaitTimeout,
	})

	frame, outcome, err := w.Wait()

	require.NoError(t, err)
	assert.Equal(t, OutcomeMatched, outcome)

	var got map[string]any
	require.NoError(t, json.Unmarshal(frame, &got))
	assert.Equal(t, "work_session", got["event_type"])
	payload, ok := got["payload"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "approved", payload["sub_type"])
	// A server frame never carries source; it is how a consumer tells the two apart.
	assert.Equal(t, "catch_up", payload["source"])
}

func TestWaiter_ClassifiesFrameSubTypes(t *testing.T) {
	tests := []struct {
		name    string
		subType string
		want    Outcome
	}{
		{name: "ok sub type", subType: "approved", want: OutcomeMatched},
		{name: "fail sub type", subType: "rejected", want: OutcomeFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := fmt.Sprintf(`{"event_type":"work_session","payload":{"sub_type":%q}}`, tt.subType)

			ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(frame))
				holdOpen(conn, 0)
			})

			w := newTestWaiter(t, ts, WaitOptions{
				OK:      []string{"approved"},
				Fail:    []string{"rejected"},
				Timeout: testWaitTimeout,
			})

			got, outcome, err := w.Wait()

			require.NoError(t, err)
			assert.Equal(t, tt.want, outcome)
			assert.JSONEq(t, frame, string(got), "the frame must reach the caller verbatim")
		})
	}
}

func TestWaiter_IgnoresAnUnrelatedSubType(t *testing.T) {
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event_type":"work_session","payload":{"sub_type":"extended"}}`))
		holdOpen(conn, 0)
	})

	w := newTestWaiter(t, ts, WaitOptions{
		OK:      []string{"approved"},
		Fail:    []string{"rejected"},
		Timeout: testShortTimeout,
	})

	_, outcome, err := w.Wait()

	require.NoError(t, err)
	assert.Equal(t, OutcomeTimeout, outcome, "a sub type in neither set must not end the wait")
}

func TestWaiter_UnparseableFrameDoesNotEndTheWait(t *testing.T) {
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, _ int32) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("not json at all"))
		holdOpen(conn, 0)
	})

	w := newTestWaiter(t, ts, WaitOptions{OK: []string{"approved"}, Timeout: testShortTimeout})

	_, outcome, err := w.Wait()

	require.NoError(t, err)
	assert.Equal(t, OutcomeTimeout, outcome)
}

func TestWaiter_StopCancelsTheWait(t *testing.T) {
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, holdOpen)

	w := newTestWaiter(t, ts, WaitOptions{OK: []string{"approved"}, Timeout: time.Minute})

	go func() {
		require.True(t, w.WaitConnected(testWaitTimeout))
		w.Stop()
	}()

	_, outcome, err := w.Wait()

	require.NoError(t, err)
	assert.Equal(t, OutcomeCanceled, outcome)
}

func TestWaiter_CatchUpErrorIsReportedButDoesNotEndTheWait(t *testing.T) {
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, holdOpen)

	w := newTestWaiter(t, ts, WaitOptions{
		CatchUp: func() (string, error) { return "", errors.New("rest is down") },
		OK:      []string{"approved"},
		Timeout: testShortTimeout,
	})

	_, outcome, err := w.Wait()

	require.NoError(t, err)
	// The subscription already stands; a failed catch-up is not worth throwing it away.
	assert.Equal(t, OutcomeTimeout, outcome)

	select {
	case reported := <-w.CatchUpFailed():
		assert.Contains(t, reported.Error(), "rest is down")
	case <-time.After(testWaitTimeout):
		t.Fatal("a catch-up failure must be reported to the caller")
	}
}

func TestWaiter_RejectedSubscriptionSurfacesTheServerMessage(t *testing.T) {
	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysRejected, holdOpen)

	w := newTestWaiter(t, ts, WaitOptions{OK: []string{"approved"}, Timeout: testWaitTimeout})

	_, outcome, err := w.Wait()

	require.Error(t, err)
	assert.Equal(t, OutcomeError, outcome)
	assert.Contains(t, err.Error(), "failed to subscribe to work_session events")
}

func TestWaiter_ReconnectRerunsCatchUp(t *testing.T) {
	var catchUps atomic.Int32

	ts, _, _ := newWatcherTestServer(t, alwaysCreated, alwaysUpgrade, alwaysCreated, func(conn *websocket.Conn, n int32) {
		if n == 1 {
			// Drop the first connection: the token is single-use, so the watcher
			// provisions a new session and re-subscribes.
			_ = conn.Close()
			return
		}
		holdOpen(conn, n)
	})

	w := newTestWaiter(t, ts, WaitOptions{
		CatchUp: func() (string, error) {
			catchUps.Add(1)
			return "pending", nil
		},
		OK:      []string{"approved"},
		Timeout: 2 * time.Second,
	})

	_, outcome, err := w.Wait()

	require.NoError(t, err)
	assert.Equal(t, OutcomeTimeout, outcome)
	// Events published during the outage are gone; re-running catch-up is the only recovery.
	assert.GreaterOrEqual(t, catchUps.Load(), int32(2), "a reconnect must re-run catch-up")
}
