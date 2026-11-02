package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	sudoGrantsURL = "/api/sudo/grants/"

	sudoHandshakeTimeout = 10 * time.Second

	// sudoOutageWarning is asserted on by the tests, so it lives here rather than
	// inline at the one place that prints it.
	sudoOutageWarning = "Sudo MFA listener disconnected; retrying..."

	// defaultMFAPollInterval is how often the listener retries the grant
	// verification while it waits for MFA.
	defaultMFAPollInterval = 500 * time.Millisecond

	// defaultMFAPollTimeout is the longest the listener waits for MFA.
	// The server expires the pending sudo grant after a short window; the buffer
	// over that keeps a slow browser MFA from racing the expiry.
	defaultMFAPollTimeout = 60 * time.Second
)

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
// ac is shared with the terminal WebSocket goroutines, which is safe because
// http.Client is concurrency-safe and mfaMu serializes token refresh and grant
// verification, so only one MFA flow runs at a time.
//
// Event channel tokens are single-use, so every dial provisions its own session
// and re-subscribes once connected.
type SudoListener struct {
	*wsListener
	ac         *client.AlpaconClient
	serverName string
	sessionID  string
	mfaMu      sync.Mutex // serializes handleSudoMFA so only one MFA flow runs at a time
	// pollInterval and pollTimeout bound retryVerifyUntilMFA. Fields rather than
	// package vars: the poll runs on a goroutine that can outlive the test which
	// started it, and a test shortening a shared knob would race the next one.
	pollInterval time.Duration
	pollTimeout  time.Duration

	stateMu    sync.Mutex // guards channelID, subscribed, warned, err
	channelID  string
	subscribed bool
	warned     bool
	err        error
}

// NewSudoListener creates a SudoListener but does not connect yet. ac may be nil
// only for a test that never calls Start: MFA request frames are then dropped, but
// provisioning a session on the listen goroutine would dereference it.
func NewSudoListener(ac *client.AlpaconClient, serverName, sessionID string) *SudoListener {
	sl := &SudoListener{
		ac:           ac,
		serverName:   serverName,
		sessionID:    sessionID,
		pollInterval: defaultMFAPollInterval,
		pollTimeout:  defaultMFAPollTimeout,
	}
	sl.wsListener = newProvisionedWSListener(ac, sl.provisionSession, sudoHandshakeTimeout)
	sl.handleFrame = sl.handleMessage
	sl.onConnected = sl.subscribe
	sl.onDialFailed = sl.announceOutage
	return sl
}

// Err returns the last reason the listener could not connect, so a caller that only
// sees WaitConnected time out can report that instead of a bare timeout.
func (sl *SudoListener) Err() error {
	sl.stateMu.Lock()
	defer sl.stateMu.Unlock()
	return sl.err
}

func (sl *SudoListener) provisionSession() (*EventSessionResponse, error) {
	session, err := CreateEventSession(sl.ac)
	if err != nil {
		return nil, sl.fail(err)
	}

	sl.stateMu.Lock()
	sl.channelID = session.ChannelID
	sl.stateMu.Unlock()

	return session, nil
}

func (sl *SudoListener) subscribe() error {
	sl.stateMu.Lock()
	channelID := sl.channelID
	sl.stateMu.Unlock()

	if err := SubscribeEvent(sl.ac, channelID, EventTypeSudo, sl.sessionID); err != nil {
		return sl.fail(err)
	}

	sl.stateMu.Lock()
	sl.subscribed = true
	// A recovered channel means the next outage is a new one, worth announcing again.
	sl.warned = false
	sl.err = nil
	sl.stateMu.Unlock()

	return nil
}

func (sl *SudoListener) announceOutage(cause error) {
	_ = sl.fail(cause)
}

// fail decides what a failed connect attempt means. Before the first subscribe a fatal
// 4xx is a bad session or an expired login: the listener stops silently, carrying the
// reason for websh to report. After it, the failure is an outage announced once, in CRLF
// for the raw-mode terminal, and left to the reconnect loop. Every cause is recorded, so
// Err() after a timeout gives the transport's own words.
func (sl *SudoListener) fail(cause error) error {
	sl.stateMu.Lock()
	sl.err = cause
	fatal := !sl.subscribed && isFatalRequestError(cause)
	announce := sl.subscribed && !sl.warned
	if announce {
		sl.warned = true
	}
	sl.stateMu.Unlock()

	if fatal {
		sl.Stop()
		return cause
	}

	if announce {
		select {
		case <-sl.done:
			// Stop cancels neither an in-flight dial nor a session create, so a
			// failure landing after it must not claim anything is still retrying.
		default:
			_, _ = fmt.Fprintf(os.Stderr, "\r\n%s\r\n", utils.Yellow(sudoOutageWarning))
		}
	}

	return cause
}

func (sl *SudoListener) handleMessage(message []byte) {
	var event sudoMFAEvent
	if err := json.Unmarshal(message, &event); err != nil {
		return
	}

	if event.Payload.Type != "auth" || event.Payload.Query != "mfa_request" {
		return
	}

	// Handle MFA on its own goroutine so the read loop keeps answering pings during
	// the wait; mfaMu keeps it to one flow at a time.
	go sl.handleSudoMFA(event)
}

func (sl *SudoListener) handleSudoMFA(event sudoMFAEvent) {
	// Every step below dereferences ac, which is nil only in tests.
	if sl.ac == nil {
		return
	}

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
	// A failed refresh does not skip the attempt: an API or service token has no
	// refresh token, and the verify call is what reports why it cannot prove MFA.
	_ = sl.ac.RefreshToken()
	if err := sl.verifySudoGrant(grantID); err == nil {
		return
	} else if sl.reportCredentialCannotProveMFA(err) {
		return
	}

	// The CLI-specific MFA link (location=cli) credits the MFA to this client, which
	// the retried verification is checked against.
	mfaURL, err := mfa.GetMFALinkByServerName(sl.ac, sl.serverName)
	if err != nil {
		if sl.reportCredentialCannotProveMFA(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "\r\n\033[31mFailed to get MFA link: %s\033[0m\r\n%s", err, utils.RawTerminalHintLines(err))
		return
	}

	fmt.Fprintf(os.Stderr, "\r\n\033[33mSudo MFA required. Opening browser...\033[0m\r\n")
	fmt.Fprintf(os.Stderr, "%s\r\n", mfaURL)
	utils.OpenBrowser(mfaURL)

	if err := sl.retryVerifyUntilMFA(grantID); err != nil {
		if errors.Is(err, errMFAWaitEnded) {
			fmt.Fprintf(os.Stderr, "\r\n\033[31mMFA verification timed out. Please re-run the sudo command.\033[0m\r\n")
			return
		}
		if sl.reportCredentialCannotProveMFA(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "\r\n\033[31mSudo verification failed: %s\033[0m\r\n", err)
		return
	}
}

// reportCredentialCannotProveMFA prints the sign-in hint and returns true when
// err is the server's refusal of a credential that cannot complete sudo MFA.
// Opening a browser or polling cannot change that, so the caller stops there.
// The server's hint, when the refusal carries one, replaces the CLI's own.
func (sl *SudoListener) reportCredentialCannotProveMFA(err error) bool {
	if code, _ := utils.ParseErrorResponse(err); code != utils.SudoVerifyCredentialCannotProveMFA {
		return false
	}
	fmt.Fprint(os.Stderr, credentialCannotProveMFAText(utils.RefusalHint(err)))
	return true
}

// credentialCannotProveMFAText renders the refusal for a websh terminal in raw
// mode, so every line ends in \r\n.
func credentialCannotProveMFAText(hint string) string {
	if hint != "" {
		return fmt.Sprintf("\r\n\033[31mSudo MFA cannot be completed with this credential.\033[0m\r\nHint: %s\r\n", hint)
	}
	return "\r\n\033[31mSudo MFA cannot be completed with this credential: sign in with `alpacon login` to complete sudo MFA.\033[0m\r\n"
}

// errMFAWaitEnded is what retryVerifyUntilMFA returns when the wait ran out, or
// the listener stopped, while the server was still refusing for MFA.
var errMFAWaitEnded = errors.New("MFA verification did not complete in time")

// retryVerifyUntilMFA retries the grant verification until the server stops
// refusing it for MFA. The step-up link names this client, so once MFA is done in
// the browser the next verification passes on the same access token, with no
// completion probe or token refresh in between.
//
// Only a typed auth_mfa_required keeps the wait going, plus up to
// utils.MaxConsecutivePollFailures attempts in a row the server never acted on
// (utils.IsUnprocessedRequestError), whose Retry-After holds the next attempt back up
// to the poll backoff cap. Any other answer is returned as is. pollTimeout bounds the wait.
func (sl *SudoListener) retryVerifyUntilMFA(grantID string) error {
	// A fixed interval, not utils.NextPollTick: the buffer defaultMFAPollTimeout
	// keeps over the server's pending-grant expiry is what this wait is built on,
	// and a gap that widens to ten ticks at the tail spends that buffer against a
	// person who is still in the browser.
	timeout := time.After(sl.pollTimeout)
	ticker := time.NewTicker(sl.pollInterval)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case <-sl.done:
			return errMFAWaitEnded
		case <-timeout:
			return errMFAWaitEnded
		case <-ticker.C:
			err := sl.verifySudoGrant(grantID)
			if err == nil {
				return nil
			}
			switch {
			case utils.StructuredErrorCode(err) == utils.AuthMFARequired:
				failures = 0
			case utils.IsUnprocessedRequestError(err):
				failures++
				if failures >= utils.MaxConsecutivePollFailures {
					return err
				}
				// A longer Retry-After waits out all but one interval, and the
				// ticker restarts so the next attempt lands one interval later.
				if wait := utils.NextPollBackoff(sl.pollInterval, 0, utils.RetryAfter(err)); wait > sl.pollInterval {
					select {
					case <-sl.done:
						return errMFAWaitEnded
					case <-timeout:
						return errMFAWaitEnded
					case <-time.After(wait - sl.pollInterval):
						ticker.Reset(sl.pollInterval)
					}
				}
			default:
				return err
			}
		}
	}
}

func (sl *SudoListener) verifySudoGrant(grantID string) error {
	pathID, err := api.RequireResponseID(grantID, "sudo grant")
	if err != nil {
		return err
	}
	endpoint := api.IDURL(sudoGrantsURL, pathID, "verify", nil)

	_, err = sl.ac.SendPostRequest(endpoint, struct{}{})
	if err != nil {
		return utils.MarkSubmission(fmt.Errorf("failed to verify sudo grant: %w", err))
	}

	return nil
}
