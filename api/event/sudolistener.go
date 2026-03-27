package event

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/gorilla/websocket"
)

const (
	sudoApproveURL = "/api/approvals/sudo-policies/self-approve/"

	// mfaPollingInterval is how often we check if MFA is completed.
	mfaPollingInterval = 2 * time.Second

	// mfaPollingTimeout is the maximum time to wait for MFA completion.
	// Server expires the approval request after 30s, but we give extra buffer.
	mfaPollingTimeout = 60 * time.Second

	// reconnectBaseDelay is the initial delay for WebSocket reconnection.
	reconnectBaseDelay = 1 * time.Second

	// reconnectMaxDelay is the maximum delay between reconnection attempts.
	reconnectMaxDelay = 30 * time.Second
)

// sudoMFAEvent represents the MFA request payload from the event WebSocket.
type sudoMFAEvent struct {
	Payload struct {
		Type              string `json:"type"`
		Query             string `json:"query"`
		ApprovalRequestID string `json:"approval_request_id"`
		MfaURL            string `json:"mfa_url"`
		Command           string `json:"command"`
		SessionID         string `json:"session_id"`
	} `json:"payload"`
}

// selfApproveRequest is sent to approve a sudo request after MFA.
type selfApproveRequest struct {
	ApprovalRequestID string `json:"approval_request_id"`
}

// SudoListener listens for sudo MFA events on the event WebSocket
// and handles the browser-based MFA flow.
//
// The AlpaconClient (ac) is shared with the terminal WebSocket goroutines.
// http.Client is concurrency-safe. Token refresh and self-approve are serialized
// by mfaMu so only one MFA flow runs at a time.
type SudoListener struct {
	ac           *client.AlpaconClient
	serverName   string
	wsURL        string
	wsHeader     http.Header
	done         chan struct{}
	stopped      chan struct{} // closed when listenLoop exits
	connected    chan struct{} // closed after first successful WebSocket connection
	connectOnce  sync.Once
	closeOnce    sync.Once
	mu           sync.Mutex
	conn         *websocket.Conn
	mfaMu        sync.Mutex // serializes handleSudoMFA so only one MFA flow runs at a time
}

// NewSudoListener creates a SudoListener but does not connect yet.
func NewSudoListener(ac *client.AlpaconClient, wsURL, serverName string) *SudoListener {
	return &SudoListener{
		ac:         ac,
		serverName: serverName,
		wsURL:      wsURL,
		wsHeader:   ac.SetWebsocketHeader(),
		done:       make(chan struct{}),
		stopped:    make(chan struct{}),
		connected:  make(chan struct{}),
	}
}

// Start begins listening for sudo MFA events in a background goroutine.
// It automatically reconnects on disconnection. Call Stop() to shut down.
func (sl *SudoListener) Start() {
	go func() {
		defer close(sl.stopped)
		sl.listenLoop()
	}()
}

// WaitConnected blocks until the WebSocket connection is established or the
// timeout expires. Returns true if connected, false on timeout or shutdown.
func (sl *SudoListener) WaitConnected(timeout time.Duration) bool {
	select {
	case <-sl.connected:
		return true
	case <-sl.done:
		return false
	case <-time.After(timeout):
		return false
	}
}

// Stop signals the listener to shut down and closes the WebSocket connection
// to unblock any pending ReadMessage call.
func (sl *SudoListener) Stop() {
	sl.closeOnce.Do(func() {
		close(sl.done)
		sl.mu.Lock()
		if sl.conn != nil {
			_ = sl.conn.Close()
		}
		sl.mu.Unlock()
	})
}

func (sl *SudoListener) listenLoop() {
	delay := reconnectBaseDelay

	for {
		select {
		case <-sl.done:
			return
		default:
		}

		connected, err := sl.connectAndListen()
		if err == nil {
			return
		}

		// Reset backoff if we had a successful connection that later dropped
		if connected {
			delay = reconnectBaseDelay
		}

		select {
		case <-sl.done:
			return
		case <-time.After(delay):
			delay *= 2
			if delay > reconnectMaxDelay {
				delay = reconnectMaxDelay
			}
		}
	}
}

// connectAndListen dials the event WebSocket and reads messages until
// the connection drops or Stop() is called. Returns (true, err) if the
// connection was established, (false, err) if the dial itself failed.
func (sl *SudoListener) connectAndListen() (connected bool, err error) {
	conn, _, dialErr := websocket.DefaultDialer.Dial(sl.wsURL, sl.wsHeader)
	if dialErr != nil {
		return false, fmt.Errorf("event websocket connection failed: %w", dialErr)
	}

	sl.mu.Lock()
	sl.conn = conn
	sl.mu.Unlock()

	// Signal that we have successfully connected (first time only)
	sl.connectOnce.Do(func() { close(sl.connected) })

	defer func() {
		sl.mu.Lock()
		sl.conn = nil
		sl.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		select {
		case <-sl.done:
			return true, nil
		default:
		}

		_, message, readErr := conn.ReadMessage()
		if readErr != nil {
			select {
			case <-sl.done:
				return true, nil
			default:
			}
			return true, fmt.Errorf("event websocket read error: %w", readErr)
		}

		sl.handleMessage(message)
	}
}

func (sl *SudoListener) handleMessage(message []byte) {
	var event sudoMFAEvent
	if err := json.Unmarshal(message, &event); err != nil {
		return
	}

	if event.Payload.Type != "auth" || event.Payload.Query != "mfa_request" {
		return
	}

	// Handle MFA in a separate goroutine so the read loop can continue
	// processing WebSocket pings and other messages during the polling wait.
	// mfaMu ensures only one MFA flow runs at a time to avoid concurrent
	// token refresh and duplicate approval calls.
	go sl.handleSudoMFA(event)
}

func (sl *SudoListener) handleSudoMFA(event sudoMFAEvent) {
	sl.mfaMu.Lock()
	defer sl.mfaMu.Unlock()

	approvalID := event.Payload.ApprovalRequestID
	if approvalID == "" {
		return
	}

	// Fast path: if MFA is already completed (e.g., recent sudo in another
	// terminal), skip the browser and approve immediately.
	if err := sl.ac.RefreshToken(); err == nil {
		if err := sl.selfApprove(approvalID); err == nil {
			return
		}
	}

	// Slow path: open browser for MFA verification.
	// Use CLI-specific MFA URL (location=cli) so the server persists
	// MFACompletion to DB for polling.
	mfaURL, err := mfa.GetMFALinkForSudo(sl.ac, sl.serverName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\r\n\033[31mFailed to get MFA link: %s\033[0m\r\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "\r\n\033[33mSudo MFA required. Opening browser...\033[0m\r\n")
	fmt.Fprintf(os.Stderr, "  %s\r\n", mfaURL)
	utils.OpenBrowser(mfaURL)

	// Poll for MFA completion
	completed := sl.pollMFACompletion()
	if !completed {
		fmt.Fprintf(os.Stderr, "\r\n\033[31mMFA verification timed out. Please re-run the sudo command.\033[0m\r\n")
		return
	}

	// MFA completed — refresh token so server sees updated MFA claims
	if err := sl.ac.RefreshToken(); err != nil {
		fmt.Fprintf(os.Stderr, "\r\n\033[31mFailed to refresh access token after MFA: %s\033[0m\r\n", err)
		return
	}

	if err := sl.selfApprove(approvalID); err != nil {
		fmt.Fprintf(os.Stderr, "\r\n\033[31mSudo approval failed: %s\033[0m\r\n", err)
		return
	}
}

func (sl *SudoListener) pollMFACompletion() bool {
	timeout := time.After(mfaPollingTimeout)
	ticker := time.NewTicker(mfaPollingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sl.done:
			return false
		case <-timeout:
			return false
		case <-ticker.C:
			completed, err := mfa.CheckMFACompletion(sl.ac)
			if err != nil {
				continue
			}
			if completed {
				return true
			}
		}
	}
}

func (sl *SudoListener) selfApprove(approvalRequestID string) error {
	req := &selfApproveRequest{
		ApprovalRequestID: approvalRequestID,
	}

	_, err := sl.ac.SendPostRequest(sudoApproveURL, req)
	if err != nil {
		return fmt.Errorf("failed to self-approve sudo request: %w", err)
	}

	return nil
}
