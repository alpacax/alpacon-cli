package worksession

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestPollForApproval_TerminalStatusesAreDistinguishable(t *testing.T) {
	t.Parallel()
	// A rejection and a network failure must not collapse into the same exit code:
	// an agent reading only $? would retry a rejected request forever.
	tests := []struct {
		status  string
		wantMsg string
	}{
		{status: "rejected", wantMsg: "work session was rejected"},
		{status: "expired", wantMsg: "work session expired while waiting for approval"},
		{status: "revoked", wantMsg: "work session was revoked"},
		{status: "cancelled", wantMsg: "work session was cancelled"},
		{status: "completed", wantMsg: "work session was completed unexpectedly"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"ws-uuid","status":%q}`, tt.status)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			_, err := pollForApproval(ac, "ws-uuid", false, time.Millisecond, time.Second)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)

			var terminal *terminalWaitError
			assert.ErrorAs(t, err, &terminal, "a settled status must be typed so the caller can exit 6")
		})
	}
}

func TestPollForApproval_WidensTheGapAsTheWaitAges(t *testing.T) {
	var polls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&polls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-1","status":"pending"}`))
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	// 1ms base tick: the fast window is the first 10ms, then the gap is 5ms.
	_, err := pollForApproval(ac, "ws-1", false, time.Millisecond, 60*time.Millisecond)

	require.Error(t, err)
	got := atomic.LoadInt32(&polls)
	assert.Less(t, got, int32(40), "a fixed 1ms tick would poll about 60 times")
}

func TestPollForApproval_RejectionNamesTheSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-1","status":"rejected"}`))
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := pollForApproval(ac, "ws-1", false, time.Millisecond, time.Second)

	var terminal *terminalWaitError
	require.ErrorAs(t, err, &terminal)
	assert.Equal(t, "ws-1", terminal.sessionID)
	assert.Contains(t, err.Error(), "ws-1")
}

func TestPollForApproval_APIFailureIsNotTerminal(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := pollForApproval(ac, "ws-uuid", false, time.Millisecond, time.Second)

	require.Error(t, err)
	var terminal *terminalWaitError
	// A 500 is a transient failure, not a settled outcome—never exit 6.
	assert.NotErrorAs(t, err, &terminal)
}

// The session is created and pending when the polls run out, so giving up must
// exit on the pending contract like the timeout does: exit 1 reads as retryable
// and an agent answers it with a second session and a second approval request.
func TestPollForApproval_GivingUpIsPending(t *testing.T) {
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	var err error
	_, stderr := testutil.CaptureOutput(t, func() {
		_, err = pollForApproval(ac, "ws-uuid", false, time.Millisecond, time.Minute)
	})

	var pending *pendingWaitError
	require.ErrorAs(t, err, &pending)
	assert.Equal(t, utils.MaxConsecutivePollFailures, requests, "the bound must stop the wait, not the deadline")
	// The transport detail rides the warning line rather than the exit code.
	assert.Contains(t, stderr, "gave up after 5 failed polls (server returned an empty error response)")
}

func TestPollForApproval_RidesThroughATransientFailure(t *testing.T) {
	t.Parallel()
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-uuid","status":"approved"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	session, err := pollForApproval(ac, "ws-uuid", false, time.Millisecond, time.Second)

	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, "approved", session.Status)
	assert.Equal(t, 2, requests, "the wait must retry the failed poll, not end on it")
}

// A body the server labels JSON but does not write as JSON reaches the warning
// verbatim through parseAPIErrorPayload, so an escape sequence in it would
// otherwise be written straight to the terminal.
func TestPollForApproval_WarningSanitizesServerText(t *testing.T) {
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("\x1b[2Kbad gateway page"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-uuid","status":"approved"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	var err error
	_, stderr := testutil.CaptureOutput(t, func() {
		_, err = pollForApproval(ac, "ws-uuid", false, time.Millisecond, time.Second)
	})

	require.NoError(t, err)
	assert.NotContains(t, stderr, "\x1b[2K")
	assert.Contains(t, stderr, "bad gateway page")
}

func TestPollForApproval_FatalClientErrorEndsTheWaitAtOnce(t *testing.T) {
	t.Parallel()
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := pollForApproval(ac, "ws-uuid", false, time.Millisecond, time.Minute)

	require.Error(t, err)
	assert.Equal(t, 1, requests, "a 404 must not be retried")
}

func TestPollForApproval_TimeoutIsNotTerminal(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-uuid","status":"pending"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := pollForApproval(ac, "ws-uuid", false, time.Millisecond, 50*time.Millisecond)

	require.Error(t, err)
	// Still pending is exit 4's territory, not the settled-negative code.
	var terminal *terminalWaitError
	assert.NotErrorAs(t, err, &terminal)
	var pending *pendingWaitError
	assert.ErrorAs(t, err, &pending)
}

// A 429 must not starve the wait: the session may still be pending, only the
// status GET refused. Without the deadline extension, the third 429 (arriving
// right at the original deadline) would end the wait with "timed out" before
// the fourth request—the one that finally reports "approved"—ever fires.
func TestPollForApproval_ThrottleExtendsTheDeadline(t *testing.T) {
	const timeout = 30 * time.Millisecond
	const interval = 10 * time.Millisecond

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests < 4 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-uuid","status":"approved"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	start := time.Now()
	session, err := pollForApproval(ac, "ws-uuid", false, interval, timeout)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, "approved", session.Status)
	assert.Equal(t, 4, requests)
	assert.GreaterOrEqual(t, elapsed, timeout, "the throttle extension must carry the wait past the original deadline")
}

// A run of 429s must not end the wait through the failed-poll cap. That cap is
// for a server the CLI cannot reach; a 429 is the server answering, and the
// throttle budget plus the deadline are what bound it.
func TestPollForApproval_SustainedThrottleDoesNotTripTheFailureCap(t *testing.T) {
	const timeout = 150 * time.Millisecond
	const interval = 2 * time.Millisecond
	throttled := utils.MaxConsecutivePollFailures + 1

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= throttled {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-uuid","status":"approved"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	session, err := pollForApproval(ac, "ws-uuid", false, interval, timeout)

	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, throttled+1, requests)
}

// The throttle budget is spent, not re-earned. Reading the same pending status
// again is no more progress than the 429 before it, so a wait that keeps being
// throttled must still end within one timeout of extensions.
func TestPollForApproval_APendingReadDoesNotRefillTheThrottleBudget(t *testing.T) {
	const timeout = 100 * time.Millisecond
	const interval = time.Millisecond

	requests := 0
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
		requests++
		// A throttled stretch long enough to spend the whole allowance, then one
		// clean read of a session sitting exactly where it was.
		if requests%8 != 0 {
			return http.StatusTooManyRequests, ""
		}
		return http.StatusOK, `{"id":"ws-uuid","status":"pending"}`
	})}

	var elapsed time.Duration
	var err error
	testutil.CaptureOutput(t, func() {
		synctest.Test(t, func(*testing.T) {
			start := time.Now()
			_, err = pollForApproval(ac, "ws-uuid", false, interval, timeout)
			elapsed = time.Since(start)
		})
	})

	require.Error(t, err)
	assert.Less(t, elapsed, utils.ThrottleCeiling(timeout, interval), "the wait outran one timeout of extensions, so the budget was refilled rather than spent")
}

// The per-poll "Poll failed" line belongs to failures the cap still counts. A
// throttled run is uncapped, so it warns once and then holds its peace.
func TestPollForApproval_ThrottleWarnsOnce(t *testing.T) {
	const timeout = 150 * time.Millisecond
	const interval = 2 * time.Millisecond
	throttled := utils.MaxConsecutivePollFailures + 1

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= throttled {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-uuid","status":"approved"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		_, _ = pollForApproval(ac, "ws-uuid", false, interval, timeout)
	})

	assert.Equal(t, 1, strings.Count(stderr, "rate limited by the server"))
	assert.NotContains(t, stderr, "Poll failed")
}

// The progress line reports how long the caller has been waiting, so a 429 that
// pushes the deadline out must not make it shrink. Deriving it from what is left
// before a moving deadline did exactly that: six throttled polls buy ~1.26s of
// extension, and the wait then reported 0s elapsed after 1.26s of waiting.
func TestPollForApproval_ElapsedTracksWallClockAcrossAThrottleExtension(t *testing.T) {
	const timeout = 2 * time.Second
	const interval = 20 * time.Millisecond
	const throttled = 6

	requests := 0
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
		requests++
		if requests <= throttled {
			return http.StatusTooManyRequests, ""
		}
		// One pending read prints the progress line under test; the next ends the
		// wait, so the assertion does not sit out the extended deadline.
		if requests == throttled+1 {
			return http.StatusOK, `{"id":"ws-uuid","status":"pending"}`
		}
		return http.StatusOK, `{"id":"ws-uuid","status":"active"}`
	})}

	_, stderr := testutil.CaptureOutput(t, func() {
		synctest.Test(t, func(*testing.T) {
			_, _ = pollForApproval(ac, "ws-uuid", false, interval, timeout)
		})
	})

	// Six throttled polls buy ~1.26s, so the window the line reports against is
	// the extended 3s rather than the 2s the flag asked for.
	assert.Contains(t, stderr, "1s elapsed of 3s")
	assert.NotContains(t, stderr, "0s elapsed of")
}

// Guards the attempt-count regression: at interval=10ms the old logic returned
// after ~20ms (no sleep after the final attempt) instead of the full 30ms.
func TestPollForApproval_WaitsFullTimeout(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ses-p","status":"pending"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	timeout := 30 * time.Millisecond
	start := time.Now()
	_, err := pollForApproval(ac, "ses-p", false, 10*time.Millisecond, timeout)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.GreaterOrEqual(t, elapsed, timeout)
}

func TestParseExpiryFlag_ExpiresIn(t *testing.T) {
	t.Parallel()
	before := time.Now()
	result, err := parseExpiryFlag("2h", "")
	after := time.Now()

	require.NoError(t, err)
	parsed, parseErr := time.Parse(time.RFC3339, result)
	require.NoError(t, parseErr)
	assert.True(t, parsed.After(before.Add(2*time.Hour-time.Second)))
	assert.True(t, parsed.Before(after.Add(2*time.Hour+time.Second)))
}

func TestParseExpiryFlag_ExpiresAt(t *testing.T) {
	t.Parallel()
	ts := "2026-12-31T23:59:59Z"
	result, err := parseExpiryFlag("", ts)
	require.NoError(t, err)
	assert.Equal(t, ts, result)
}

func TestParseExpiryFlag_BothProvided(t *testing.T) {
	t.Parallel()
	_, err := parseExpiryFlag("2h", "2026-12-31T23:59:59Z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestParseExpiryFlag_NeitherProvided(t *testing.T) {
	t.Parallel()
	_, err := parseExpiryFlag("", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestParseExpiryFlag_InvalidDuration(t *testing.T) {
	t.Parallel()
	_, err := parseExpiryFlag("2hours", "")
	assert.Error(t, err)
}

func TestParseExpiryFlag_ZeroDuration(t *testing.T) {
	t.Parallel()
	_, err := parseExpiryFlag("0s", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive duration")
}

func TestParseExpiryFlag_NegativeDuration(t *testing.T) {
	t.Parallel()
	_, err := parseExpiryFlag("-1h", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive duration")
}

func TestResolveWaitTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		wait            bool
		waitApprovalRaw string
		waitApprovalSet bool
		want            time.Duration
		wantErr         bool
	}{
		{name: "bare --wait uses the default", wait: true, want: 5 * time.Minute},
		{name: "no wait flags", want: 0},
		{name: "--wait-approval implies wait", waitApprovalRaw: "30m", waitApprovalSet: true, want: 30 * time.Minute},
		{name: "--wait-approval wins over bare --wait", wait: true, waitApprovalRaw: "30m", waitApprovalSet: true, want: 30 * time.Minute},
		{name: "invalid duration", wait: true, waitApprovalRaw: "10minutes", waitApprovalSet: true, wantErr: true},
		{name: "zero duration", wait: true, waitApprovalRaw: "0s", waitApprovalSet: true, wantErr: true},
		{name: "negative duration", wait: true, waitApprovalRaw: "-5m", waitApprovalSet: true, wantErr: true},
		// --wait-approval= must fail like exec's parser, not silently fall back to
		// bare --wait (or to no wait at all, which would drop the requested wait).
		{name: "explicit empty value without --wait", waitApprovalSet: true, wantErr: true},
		{name: "explicit empty value with --wait", wait: true, waitApprovalSet: true, wantErr: true},
		{name: "explicit blank value with --wait", wait: true, waitApprovalRaw: "   ", waitApprovalSet: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := resolveWaitTimeout(tt.wait, tt.waitApprovalRaw, tt.waitApprovalSet)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, d)
		})
	}
}

func TestValidateAgentScopes_AgentWithWebsh(t *testing.T) {
	t.Parallel()
	err := validateAgentScopes("agent", []string{"command", "websh"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "\"websh\" is not allowed")
}

func TestValidateAgentScopes_AgentWithoutWebsh(t *testing.T) {
	t.Parallel()
	err := validateAgentScopes("agent", []string{"command", "webftp"})
	assert.NoError(t, err)
}

func TestValidateAgentScopes_UserWithWebsh(t *testing.T) {
	t.Parallel()
	err := validateAgentScopes("user", []string{"command", "websh"})
	assert.NoError(t, err)
}

func TestValidateScopeEnum(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		scopes      []string
		wantErr     bool
		wantSubstrs []string
	}{
		{
			name:    "empty input passes (handled by other validation)",
			scopes:  nil,
			wantErr: false,
		},
		{
			name:    "single valid scope",
			scopes:  []string{"command"},
			wantErr: false,
		},
		{
			name:    "multiple valid scopes",
			scopes:  []string{"command", "websh", "sudo"},
			wantErr: false,
		},
		{
			name:        "single unknown scope",
			scopes:      []string{"foo"},
			wantErr:     true,
			wantSubstrs: []string{"foo", "valid:", "command", "editor", "sudo", "tunnel", "webftp", "websh"},
		},
		{
			name:        "mixed valid and unknown scopes, alphabetically sorted in message",
			scopes:      []string{"command", "zzz", "aaa"},
			wantErr:     true,
			wantSubstrs: []string{"aaa, zzz", "valid:"},
		},
		{
			name:        "case-sensitive: capitalized is rejected",
			scopes:      []string{"Command"},
			wantErr:     true,
			wantSubstrs: []string{"Command", "valid:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScopeEnum(tt.scopes)
			if tt.wantErr {
				require.Error(t, err)
				for _, s := range tt.wantSubstrs {
					assert.Contains(t, err.Error(), s)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDecideUseAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     string
		useEnabled bool
		want       useDecision
	}{
		{name: "use disabled with pending status", status: "pending", useEnabled: false, want: useDecisionNoop},
		{name: "use disabled with active status", status: "active", useEnabled: false, want: useDecisionNoop},
		{name: "use enabled with active status (superuser or post-poll)", status: "active", useEnabled: true, want: useDecisionUseNow},
		{name: "use enabled with pending status (needs --wait)", status: "pending", useEnabled: true, want: useDecisionErrorNeedsWait},
		{name: "use enabled with approved status (scheduled starts_at)", status: "approved", useEnabled: true, want: useDecisionSkipScheduled},
		{name: "use enabled with rejected status (terminal)", status: "rejected", useEnabled: true, want: useDecisionErrorNeedsWait},
		{name: "use enabled with expired status (terminal)", status: "expired", useEnabled: true, want: useDecisionErrorNeedsWait},
		{name: "use enabled with revoked status (terminal)", status: "revoked", useEnabled: true, want: useDecisionErrorNeedsWait},
		{name: "use enabled with completed status (terminal)", status: "completed", useEnabled: true, want: useDecisionErrorNeedsWait},
		{name: "use enabled with empty status (defensive)", status: "", useEnabled: true, want: useDecisionErrorNeedsWait},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, decideUseAction(tt.status, tt.useEnabled))
		})
	}
}

func TestWorkSessionCreateWaitPrintsAdvisories(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(`{"id":"ses-x","status":"pending","approval_request_id":"apr-1","expires_at":"2026-06-01T12:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/ses-x/":
			_, _ = w.Write([]byte(`{
				"id":"ses-x","status":"approved","expires_at":"2026-06-01T12:00:00Z",
				"adjustments":{"scopes":{"old":["command","websh"],"new":["command"]}},
				"recommendations":[{"id":"r1","text":"Rotate the key","severity":"high"}]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)
	resetCreateCommandState(t)
	purpose = "incident"
	createScopes = []string{"command", "websh"}
	createServers = []string{"prod"}
	expiresAt = "2026-06-01T12:00:00Z"
	wait = true

	_, stderr := testutil.CaptureOutput(t, func() {
		workSessionCreateCmd.Run(workSessionCreateCmd, nil)
	})

	assert.Contains(t, stderr, "approved")
	assert.Contains(t, stderr, "Approver adjusted your request")
	assert.Contains(t, stderr, "command, websh → command")
	assert.Contains(t, stderr, "[HIGH] Rotate the key")
}

func TestWorkSessionCreateAutoApprovedPrintsAdvisories(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(`{
				"id":"ses-x","status":"approved","expires_at":"2026-06-01T12:00:00Z",
				"adjustments":{"scopes":{"old":["command","websh"],"new":["command"]}},
				"recommendations":[{"id":"r1","text":"Rotate the key","severity":"high"}]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)
	resetCreateCommandState(t)
	purpose = "incident"
	createScopes = []string{"command", "websh"}
	createServers = []string{"prod"}
	expiresAt = "2026-06-01T12:00:00Z"

	_, stderr := testutil.CaptureOutput(t, func() {
		workSessionCreateCmd.Run(workSessionCreateCmd, nil)
	})

	assert.Contains(t, stderr, "Approver adjusted your request")
	assert.Contains(t, stderr, "command, websh → command")
	assert.Contains(t, stderr, "[HIGH] Rotate the key")
}

func TestWorkSessionCreateWaitJSONOutputIncludesAdjustments(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(`{"id":"ses-x","status":"pending","approval_request_id":"apr-1","expires_at":"2026-06-01T12:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/ses-x/":
			_, _ = w.Write([]byte(`{
				"id":"ses-x","status":"approved","expires_at":"2026-06-01T12:00:00Z",
				"adjustments":{"scopes":{"old":["command","websh"],"new":["command"]}},
				"recommendations":[{"id":"r1","text":"Rotate the key","severity":"high"}]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)
	withWorkSessionCommandJSONMode(t)
	resetCreateCommandState(t)
	purpose = "incident"
	createScopes = []string{"command", "websh"}
	createServers = []string{"prod"}
	expiresAt = "2026-06-01T12:00:00Z"
	wait = true

	stdout, _ := testutil.CaptureOutput(t, func() {
		workSessionCreateCmd.Run(workSessionCreateCmd, nil)
	})

	var got struct {
		WorkSession struct {
			Adjustments struct {
				Scopes struct {
					New []string `json:"new"`
				} `json:"scopes"`
			} `json:"adjustments"`
			Recommendations []struct {
				Severity string `json:"severity"`
			} `json:"recommendations"`
		} `json:"work_session"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, []string{"command"}, got.WorkSession.Adjustments.Scopes.New)
	require.Len(t, got.WorkSession.Recommendations, 1)
	assert.Equal(t, "high", got.WorkSession.Recommendations[0].Severity)
}

// rejectedWaitServer answers a create whose first poll comes back rejected, so a
// --wait run settles without a grant on its first tick.
func rejectedWaitServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(`{"id":"ses-x","status":"pending","approval_request_id":"apr-1","expires_at":"2026-06-01T12:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/ses-x/":
			_, _ = w.Write([]byte(`{"id":"ses-x","status":"rejected","expires_at":"2026-06-01T12:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func rejectedWaitArgs() []string {
	return []string{
		"create", "--purpose", "incident", "--scope", "command", "--server", "prod",
		"--expires-at", "2026-06-01T12:00:00Z", "--wait",
	}
}

func TestWorkSessionCreateWaitRejectedNamesTheSessionOnStderr(t *testing.T) {
	ts := rejectedWaitServer()
	defer ts.Close()

	_, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL, rejectedWaitArgs()...)

	assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "a refused wait is settled, not pending; stderr: %s", stderr)
	assert.Contains(t, stderr, "ses-x")
	assert.Contains(t, stderr, "alpacon work-session use ses-x")
	assert.Contains(t, stderr, "alpacon work-session complete ses-x")
	assert.NotContains(t, stderr, "report on https://github.com", "a reviewer saying no is not a bug to file")
}

func TestWorkSessionCreateWaitRejectedJSONCarriesTheSessionID(t *testing.T) {
	ts := rejectedWaitServer()
	defer ts.Close()

	_, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL, rejectedWaitArgs()...)

	assert.Equal(t, utils.ExitCodeNotApproved, exitCode)

	var env struct {
		OK       bool   `json:"ok"`
		ExitCode int    `json:"exit_code"`
		Message  string `json:"message"`
		Context  struct {
			Operation     string `json:"operation"`
			WorkSessionID string `json:"work_session_id"`
		} `json:"context"`
		NextActions []utils.NextAction `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(stderr), &env), "stderr: %s", stderr)
	assert.False(t, env.OK)
	assert.Equal(t, utils.ExitCodeNotApproved, env.ExitCode)
	assert.Equal(t, "ses-x", env.Context.WorkSessionID)
	require.Len(t, env.NextActions, 2)
	assert.Equal(t, "alpacon work-session use ses-x", env.NextActions[0].Command)
	assert.Equal(t, "alpacon work-session complete ses-x", env.NextActions[1].Command)
}

func TestPrintTerminalWaitErrorSanitizesTheSessionID(t *testing.T) {
	terminal := &terminalWaitError{message: "work session was rejected", sessionID: "ses-\x1b[2Kx"}

	_, stderr := testutil.CaptureOutput(t, func() {
		printTerminalWaitError(terminal, terminal)
	})

	assert.NotContains(t, stderr, "\x1b[2K")
	assert.Contains(t, stderr, "work session was rejected")
}
