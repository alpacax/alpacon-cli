package event

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	// sudoVerifyURLFmt is the sudo-grant MFA verify endpoint; the grant ID is
	// substituted into the path. Replaces the removed self-approve route.
	sudoVerifyURLFmt = "/api/sudo/grants/%s/verify/"

	// mfaPollingInterval is how often we check if MFA is completed.
	mfaPollingInterval = 500 * time.Millisecond

	// mfaPollingTimeout is the maximum time to wait for MFA completion.
	// The server expires the pending sudo grant after a short window; we keep
	// extra buffer over that so a slow browser MFA does not race the expiry.
	mfaPollingTimeout = 60 * time.Second

	sudoHandshakeTimeout = 10 * time.Second
)

// sudoMFAEvent represents the MFA request payload from the event WebSocket.
type sudoMFAEvent struct {
	Payload struct {
		Type        string `json:"type"`
		Query       string `json:"query"`
		SudoGrantID string `json:"sudo_grant_id"`
		MfaURL      string `json:"mfa_url"`
		Command     string `json:"command"`
		SessionID   string `json:"session_id"`
	} `json:"payload"`
}

// SudoListener listens for sudo MFA events on the event WebSocket
// and handles the browser-based MFA flow.
//
// The AlpaconClient (ac) is shared with the terminal WebSocket goroutines.
// http.Client is concurrency-safe. Token refresh and grant verification are
// serialized by mfaMu so only one MFA flow runs at a time.
//
// Event channel tokens are single-use, so every dial provisions its own session
// and re-subscribes once connected.
type SudoListener struct {
	*wsListener
	ac         *client.AlpaconClient
	serverName string
	sessionID  string
	mfaMu      sync.Mutex // serializes handleSudoMFA so only one MFA flow runs at a time

	stateMu    sync.Mutex // guards channelID, subscribed, warned, err
	channelID  string
	subscribed bool
	warned     bool
	err        error
}

// NewSudoListener creates a SudoListener but does not connect yet. ac may be
// nil in tests; MFA request frames are then dropped instead of dereferencing it.
func NewSudoListener(ac *client.AlpaconClient, serverName, sessionID string) *SudoListener {
	sl := &SudoListener{
		ac:         ac,
		serverName: serverName,
		sessionID:  sessionID,
	}
	sl.wsListener = newProvisionedWSListener(ac, sl.provisionSession, sudoHandshakeTimeout)
	sl.handleFrame = sl.handleMessage
	sl.onConnected = sl.subscribe
	sl.onDialFailed = sl.announceOutage
	return sl
}

// Err returns the error that stopped the listener before it ever subscribed.
func (sl *SudoListener) Err() error {
	sl.stateMu.Lock()
	defer sl.stateMu.Unlock()
	return sl.err
}

func (sl *SudoListener) provisionSession() (string, error) {
	session, err := CreateEventSession(sl.ac)
	if err != nil {
		sl.announceOutage(err)
		return "", sl.failIfFatal(err)
	}

	sl.stateMu.Lock()
	sl.channelID = session.ChannelID
	sl.stateMu.Unlock()

	return session.WebsocketURL, nil
}

func (sl *SudoListener) subscribe() error {
	sl.stateMu.Lock()
	channelID := sl.channelID
	sl.stateMu.Unlock()

	if err := SubscribeEvent(sl.ac, channelID, EventTypeSudo, sl.sessionID); err != nil {
		sl.announceOutage(err)
		return sl.failIfFatal(err)
	}

	sl.stateMu.Lock()
	sl.subscribed = true
	// A recovered channel means the next outage is a new one, worth announcing again.
	sl.warned = false
	sl.stateMu.Unlock()

	return nil
}

// failIfFatal stops the listener when retrying cannot help: a 4xx before the first
// subscribe means a bad session or an expired login, not an outage. Everything else
// is left to the reconnect loop.
func (sl *SudoListener) failIfFatal(cause error) error {
	sl.stateMu.Lock()
	fatal := !sl.subscribed && isFatalRequestError(cause)
	if fatal {
		sl.err = cause
	}
	sl.stateMu.Unlock()

	if fatal {
		sl.Stop()
	}

	return cause
}

// announceOutage reports a dropped event channel once per outage. Before the first
// subscribe the caller (websh) already handles the failure, so this stays quiet.
// The message is wrapped in CRLF because websh holds the terminal in raw mode.
func (sl *SudoListener) announceOutage(error) {
	sl.stateMu.Lock()
	quiet := !sl.subscribed || sl.warned
	if !quiet {
		sl.warned = true
	}
	sl.stateMu.Unlock()

	if quiet {
		return
	}

	_, _ = fmt.Fprint(os.Stderr, "\r\n\033[33mSudo MFA listener disconnected; retrying...\033[0m\r\n")
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
	// Every step below dereferences ac, which is nil only in tests.
	if sl.ac == nil {
		return
	}

	// Check if shutdown was requested before acquiring the lock or doing side effects
	select {
	case <-sl.done:
		return
	default:
	}

	sl.mfaMu.Lock()
	defer sl.mfaMu.Unlock()

	// Re-check after acquiring lock in case Stop() was called while waiting
	select {
	case <-sl.done:
		return
	default:
	}

	grantID := event.Payload.SudoGrantID
	if grantID == "" {
		fmt.Fprintf(os.Stderr, "\r\n\033[31mSudo MFA event is missing a grant ID; cannot verify. This likely indicates a server/CLI version mismatch.\033[0m\r\n")
		return
	}

	// Fast path: if MFA is already completed (e.g., recent sudo in another
	// terminal), skip the browser and approve immediately.
	if err := sl.ac.RefreshToken(); err == nil {
		if err := sl.verifySudoGrant(grantID); err == nil {
			return
		}
	}

	// Slow path: open browser for MFA verification.
	// Use CLI-specific MFA URL (location=cli) so the server persists
	// MFACompletion to DB for polling.
	mfaURL, err := mfa.GetMFALinkByServerName(sl.ac, sl.serverName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\r\n\033[31mFailed to get MFA link: %s\033[0m\r\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "\r\n\033[33mSudo MFA required. Opening browser...\033[0m\r\n")
	fmt.Fprintf(os.Stderr, "%s\r\n", mfaURL)
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

	if err := sl.verifySudoGrant(grantID); err != nil {
		fmt.Fprintf(os.Stderr, "\r\n\033[31mSudo verification failed: %s\033[0m\r\n", err)
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

func (sl *SudoListener) verifySudoGrant(grantID string) error {
	// PathEscape the server-issued grant ID so a stray '/' cannot redirect
	// the request to an unintended endpoint.
	endpoint := fmt.Sprintf(sudoVerifyURLFmt, url.PathEscape(grantID))

	_, err := sl.ac.SendPostRequest(endpoint, struct{}{})
	if err != nil {
		return fmt.Errorf("failed to verify sudo grant: %w", err)
	}

	return nil
}
