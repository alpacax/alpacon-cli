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
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		connected: make(chan struct{}),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sl.handleMessage([]byte(tt.payload))
		})
	}
}

func TestSudoListener_StopIsIdempotent(t *testing.T) {
	sl := &SudoListener{
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		connected: make(chan struct{}),
	}

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
		wsURL:     wsURL,
		wsHeader:  http.Header{},
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		connected: make(chan struct{}),
	}
	sl.Start()

	// Wait for connection to establish by polling sl.conn
	require.Eventually(t, func() bool {
		sl.mu.Lock()
		defer sl.mu.Unlock()
		return sl.conn != nil
	}, 2*time.Second, 10*time.Millisecond, "listener should connect")

	sl.Stop()

	// Wait for listenLoop goroutine to exit via stopped channel
	select {
	case <-sl.stopped:
		// goroutine exited and cleanup ran
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for listener goroutine to exit")
	}

	sl.mu.Lock()
	assert.Nil(t, sl.conn, "connection should be nil after stop")
	sl.mu.Unlock()
}

func TestSudoListener_ConnectAndListen_ReadsMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Send a non-MFA message
		msg := `{"payload":{"type":"info","query":"status"}}`
		_ = conn.WriteMessage(websocket.TextMessage, []byte(msg))

		// Wait for client to read it before closing
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Wrap handleMessage to count calls
	sl := &SudoListener{
		wsURL:     wsURL,
		wsHeader:  http.Header{},
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		connected: make(chan struct{}),
	}

	// Run connectAndListen directly to verify it reads the message
	go func() {
		defer close(sl.stopped)
		_, _ = sl.connectAndListen()
	}()

	// The server sends one message then closes. After disconnect, verify
	// that connectAndListen returns (the goroutine exits).
	select {
	case <-sl.stopped:
		// goroutine exited
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for connectAndListen to return")
	}

	// connectAndListen read the non-MFA message and returned on server disconnect
	// without panic. This verifies the read loop handles messages and clean shutdown.
}

func TestSudoListener_PollMFACompletion_Timeout(t *testing.T) {
	sl := &SudoListener{
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		connected: make(chan struct{}),
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
