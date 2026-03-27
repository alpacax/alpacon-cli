package event

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSudoMFAEvent_JSONRoundTrip(t *testing.T) {
	payload := sudoMFAEvent{}
	payload.Payload.Type = "auth"
	payload.Payload.Query = "mfa_request"
	payload.Payload.ApprovalRequestID = "test-approval-id"
	payload.Payload.MfaURL = "https://auth.alpacon.io/mfa?token=abc"
	payload.Payload.Command = "sudo systemctl restart nginx"
	payload.Payload.SessionID = "test-session-id"

	msg, err := json.Marshal(payload)
	require.NoError(t, err)

	var parsed sudoMFAEvent
	err = json.Unmarshal(msg, &parsed)
	assert.NoError(t, err)
	assert.Equal(t, "auth", parsed.Payload.Type)
	assert.Equal(t, "mfa_request", parsed.Payload.Query)
	assert.Equal(t, "test-approval-id", parsed.Payload.ApprovalRequestID)
	assert.Equal(t, "https://auth.alpacon.io/mfa?token=abc", parsed.Payload.MfaURL)
}

func TestSudoListener_HandleMessage_IgnoresNonMFA(t *testing.T) {
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

	sl := &SudoListener{
		done: make(chan struct{}),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			sl.handleMessage([]byte(tt.payload))
		})
	}
}

func TestSudoListener_StopIsIdempotent(t *testing.T) {
	sl := &SudoListener{done: make(chan struct{})}

	// Multiple Stop() calls should not panic
	sl.Stop()
	sl.Stop()
	sl.Stop()
}

func TestSudoListener_StopClosesConnection(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	sl := &SudoListener{
		wsURL:    wsURL,
		wsHeader: http.Header{},
		done:     make(chan struct{}),
	}
	sl.Start()

	// Wait for connection to establish
	time.Sleep(100 * time.Millisecond)

	// Stop should close the connection and the goroutine should exit
	sl.Stop()

	select {
	case <-sl.done:
		// listener has stopped
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for listener to stop")
	}

	sl.mu.Lock()
	assert.Nil(t, sl.conn, "connection should be nil after stop")
	sl.mu.Unlock()
}

func TestSudoListener_ConnectAndListen(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		msg := `{"payload":{"type":"info","query":"status"}}`
		_ = conn.WriteMessage(websocket.TextMessage, []byte(msg))

		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	sl := &SudoListener{
		wsURL:    wsURL,
		wsHeader: http.Header{},
		done:     make(chan struct{}),
	}

	sl.Start()
	time.Sleep(200 * time.Millisecond)
	sl.Stop()
}

func TestSudoListener_PollMFACompletion_Timeout(t *testing.T) {
	sl := &SudoListener{
		done: make(chan struct{}),
	}

	start := time.Now()
	go func() {
		time.Sleep(100 * time.Millisecond)
		sl.Stop()
	}()

	result := sl.pollMFACompletion()
	elapsed := time.Since(start)

	assert.False(t, result, "should return false when stopped")
	assert.Less(t, elapsed, 2*time.Second, "should exit quickly when stopped")
}
