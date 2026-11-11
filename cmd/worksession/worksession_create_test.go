package worksession

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
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
		{status: "expired", wantMsg: "work session expired before it was approved"},
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

			_, err := WaitForApproval(ac, "ws-uuid", false, time.Millisecond, time.Second)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)

			var terminal *terminalWaitError
			assert.ErrorAs(t, err, &terminal, "a settled status must be typed so the caller can exit 6")
		})
	}
}

func TestPollForApproval_WidensTheGapAsTheWaitAges(t *testing.T) {
	var polls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		polls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-1","status":"pending"}`))
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	// 1ms base tick: the fast window is the first 10ms, then the gap is 5ms.
	_, err := WaitForApproval(ac, "ws-1", false, time.Millisecond, 60*time.Millisecond)

	require.Error(t, err)
	got := polls.Load()
	assert.Less(t, got, int32(40), "a fixed 1ms tick would poll about 60 times")
}

func TestPollForApproval_RejectionNamesTheSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-1","status":"rejected"}`))
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := WaitForApproval(ac, "ws-1", false, time.Millisecond, time.Second)

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

	_, err := WaitForApproval(ac, "ws-uuid", false, time.Millisecond, time.Second)

	require.Error(t, err)
	var terminal *terminalWaitError
	// A 500 is a transient failure, not a settled outcome—never exit 6.
	assert.NotErrorAs(t, err, &terminal)
}

// Giving up on failed polls leaves the session pending, so it exits on the pending contract:
// exit 1 reads as retryable and gets answered with a second session.
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
		_, err = WaitForApproval(ac, "ws-uuid", false, time.Millisecond, time.Minute)
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

	session, err := WaitForApproval(ac, "ws-uuid", false, time.Millisecond, time.Second)

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
		_, err = WaitForApproval(ac, "ws-uuid", false, time.Millisecond, time.Second)
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

	_, err := WaitForApproval(ac, "ws-uuid", false, time.Millisecond, time.Minute)

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

	_, err := WaitForApproval(ac, "ws-uuid", false, time.Millisecond, 50*time.Millisecond)

	require.Error(t, err)
	// Still pending is exit 4's territory, not the settled-negative code.
	var terminal *terminalWaitError
	assert.NotErrorAs(t, err, &terminal)
	var pending *pendingWaitError
	assert.ErrorAs(t, err, &pending)
}

// Without the deadline extension, the third 429, at the original deadline, would end the wait
// before the fourth poll reports "approved".
func TestPollForApproval_ThrottleExtendsTheDeadline(t *testing.T) {
	const timeout = 30 * time.Millisecond
	const interval = 10 * time.Millisecond

	requests := 0
	ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, HTTPClient: testutil.StubClient(func(*http.Request) (int, string) {
		requests++
		if requests < 4 {
			return http.StatusTooManyRequests, ""
		}
		return http.StatusOK, `{"id":"ws-uuid","status":"approved"}`
	})}

	var session *wsapi.WorkSession
	var err error
	var elapsed time.Duration
	synctest.Test(t, func(*testing.T) {
		start := time.Now()
		session, err = WaitForApproval(ac, "ws-uuid", false, interval, timeout)
		elapsed = time.Since(start)
	})

	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, "approved", session.Status)
	assert.Equal(t, 4, requests)
	assert.GreaterOrEqual(t, elapsed, timeout, "the throttle extension must carry the wait past the original deadline")
}

// The failed-poll cap is for a server the CLI cannot reach; a 429 is the server answering, bounded
// by the throttle budget and the deadline instead.
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

	session, err := WaitForApproval(ac, "ws-uuid", false, interval, timeout)

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
			_, err = WaitForApproval(ac, "ws-uuid", false, interval, timeout)
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
		_, _ = WaitForApproval(ac, "ws-uuid", false, interval, timeout)
	})

	assert.Equal(t, 1, strings.Count(stderr, "rate limited by the server"))
	assert.NotContains(t, stderr, "Poll failed")
}

// The progress line's elapsed time must not shrink when a 429 pushes the deadline out, as it did
// when derived from the time left before a moving deadline.
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
			_, _ = WaitForApproval(ac, "ws-uuid", false, interval, timeout)
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
	_, err := WaitForApproval(ac, "ses-p", false, 10*time.Millisecond, timeout)
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
	err := ValidateAgentScopes("agent", []string{"command", "websh"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "\"websh\" is not allowed")
}

func TestValidateAgentScopes_AgentWithoutWebsh(t *testing.T) {
	t.Parallel()
	err := ValidateAgentScopes("agent", []string{"command", "webftp"})
	assert.NoError(t, err)
}

func TestValidateAgentScopes_UserWithWebsh(t *testing.T) {
	t.Parallel()
	err := ValidateAgentScopes("user", []string{"command", "websh"})
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
		{name: "use enabled with active status", status: "active", useEnabled: true, want: useDecisionUseNow},
		{name: "use enabled with pending status", status: "pending", useEnabled: true, want: useDecisionNeedsApproval},
		{name: "use enabled with approved status", status: "approved", useEnabled: true, want: useDecisionSkipScheduled},
		{name: "use enabled with rejected status", status: "rejected", useEnabled: true, want: useDecisionSettled},
		{name: "use enabled with expired status", status: "expired", useEnabled: true, want: useDecisionSettled},
		{name: "use enabled with revoked status", status: "revoked", useEnabled: true, want: useDecisionSettled},
		{name: "use enabled with cancelled status", status: "cancelled", useEnabled: true, want: useDecisionSettled},
		{name: "use enabled with completed status", status: "completed", useEnabled: true, want: useDecisionSettled},
		{name: "use enabled with an unknown status", status: "paused", useEnabled: true, want: useDecisionUnknownStatus},
		{name: "use enabled with an empty status", status: "", useEnabled: true, want: useDecisionUnknownStatus},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, decideUseAction(tt.status, tt.useEnabled))
		})
	}
}

func TestSettledNegativelyNamesEveryEndedStatus(t *testing.T) {
	t.Parallel()
	for _, status := range wsapi.EndedStatuses() {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			terminal := settledNegatively(status, "ses-x")
			require.NotNil(t, terminal)
			assert.Equal(t, "ses-x", terminal.sessionID)
		})
	}
}

func TestSettledNegativelyIsNilForAStatusThatCanStillBecomeActive(t *testing.T) {
	t.Parallel()
	for _, status := range []string{wsapi.StatusPending, wsapi.StatusApproved, wsapi.StatusActive, "paused", ""} {
		t.Run("status "+status, func(t *testing.T) {
			t.Parallel()
			assert.Nil(t, settledNegatively(status, "ses-x"))
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

func TestWorkSessionCreateUpdateNext(t *testing.T) {
	const line = "  alpacon work-session update ses-x --server <server,...>  # add servers to this session; include every server to keep (--server replaces the whole list); an added server may need approval\n"
	tests := []struct {
		name       string
		status     string
		extra      []string
		refused    bool
		outputJSON bool
	}{
		{name: "active", status: wsapi.StatusActive},
		{name: "approved", status: wsapi.StatusApproved},
		{name: "active with use", status: wsapi.StatusActive, extra: []string{"--use"}},
		{name: "scheduled with use", status: wsapi.StatusApproved, extra: []string{"--use"}},
		{name: "wait approved", status: wsapi.StatusApproved, extra: []string{"--wait"}},
		{name: "wait and use", status: wsapi.StatusActive, extra: []string{"--wait", "--use"}},
		{name: "refused create", refused: true},
		{name: "success JSON keeps its schema", status: wsapi.StatusActive, outputJSON: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
					if tt.refused {
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte(`{"code":"permission_denied"}`))
						return
					}
					status := tt.status
					if slices.Contains(tt.extra, "--wait") {
						status = wsapi.StatusPending
					}
					_, _ = fmt.Fprintf(w, `{"id":"ses-x","status":%q}`, status)
				case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/ses-x/":
					_, _ = fmt.Fprintf(w, `{"id":"ses-x","status":%q}`, tt.status)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(ts.Close)
			format := utils.OutputFormatTable
			if tt.outputJSON {
				format = utils.OutputFormatJSON
			}
			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), format, ts.URL, createArgs(tt.extra...)...)
			if tt.refused {
				assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "stderr: %s", stderr)
				assert.NotContains(t, stdout+stderr, "work-session update")
				return
			}
			require.Zero(t, exitCode, "stderr: %s", stderr)
			if tt.outputJSON {
				var output map[string]any
				require.NoError(t, json.Unmarshal([]byte(stdout), &output))
				assert.NotContains(t, output, "next_actions")
				assert.Empty(t, stderr)
				return
			}
			assert.Contains(t, stderr, "Next:\n"+line)
			assert.Equal(t, 1, strings.Count(stderr, line))
		})
	}
}

// An agent runs the refusal's follow-ups, so each must be a command the session can still take.
// RunUseSession refuses anything but an active session, which no terminalWaitError status can
// reach.
func TestRunUseSessionRefusesASettledSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ses-x","status":"rejected"}`))
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := RunUseSession(ac, "ses-x")

	require.Error(t, err, "a rejected session must not be attachable")
	for _, action := range terminalWaitNextActions("ses-x") {
		assert.NotContains(t, action.Command, "work-session use",
			"a refusal must not hand back a command RunUseSession will reject")
	}
}

func TestPrintTerminalWaitErrorSanitizesTheSessionID(t *testing.T) {
	terminal := &terminalWaitError{message: "work session was rejected", sessionID: "ses-\x1b[2Kx"}

	_, stderr := testutil.CaptureOutput(t, func() {
		printTerminalWaitError(terminal, terminal)
	})

	assert.NotContains(t, stderr, "\x1b[2K")
	assert.Contains(t, stderr, "work session was rejected")
}

func TestPollForApproval_EveryEndedStatusHasACase(t *testing.T) {
	t.Parallel()
	// A status IsEndedStatus accepts must end the wait, not poll until the timeout.
	for _, status := range wsapi.EndedStatuses() {
		t.Run(status, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"ws-uuid","status":%q}`, status)
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			_, err := WaitForApproval(ac, "ws-uuid", false, time.Millisecond, 50*time.Millisecond)

			var terminal *terminalWaitError
			assert.ErrorAs(t, err, &terminal, "no case for ended status %q in the create --wait switch", status)
		})
	}
}

func TestPollForApproval_TimeoutOnARefusalKeepsTheHint(t *testing.T) {
	t.Parallel()
	const hint = "Too many requests were made in a short time; wait, then retry."
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code": "api_rate_limited", "gate": "rate", "hint": "` + hint + `"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := WaitForApproval(ac, "ws-uuid", false, time.Millisecond, 30*time.Millisecond)

	require.Error(t, err)
	var pending *pendingWaitError
	require.ErrorAs(t, err, &pending)
	assert.Contains(t, err.Error(), "timed out waiting for approval")
	assert.Equal(t, []utils.NextAction{{Description: hint}}, utils.RefusalNextActions(err))
	// Only the hint goes with the timeout, never the refusal's code or status.
	code, _ := utils.ParseErrorResponse(err)
	assert.Empty(t, code)
	assert.Zero(t, utils.HTTPStatusCode(err))
}

func TestPollForApproval_PlainTimeoutCarriesNoHint(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ws-uuid","status":"pending"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := WaitForApproval(ac, "ws-uuid", false, time.Millisecond, 30*time.Millisecond)
	require.Error(t, err)
	assert.Empty(t, utils.RefusalNextActions(err))
}

func TestWorkSessionCreateUseWithoutAUsableLoginExitsOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		config      string
		wantCode    string
		wantMessage string
		noBugFooter bool
	}{
		{name: "no config file", wantCode: utils.ErrorCodeNotLoggedIn, wantMessage: "Please run 'alpacon login'"},
		{name: "config without a workspace", config: `{"workspace_url":"http://127.0.0.1:1","token":"t"}`, wantCode: utils.ErrorCodeNotLoggedIn, wantMessage: "--use requires an active workspace. Run 'alpacon login' first.", noBugFooter: true},
		{name: "unreadable config", config: "{not json", wantCode: utils.ErrorCodeCLI, wantMessage: "failed to decode config file"},
	}
	for _, tt := range tests {
		for _, outputFormat := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
			t.Run(tt.name+" "+outputFormat, func(t *testing.T) {
				t.Parallel()
				home := t.TempDir()
				if tt.config != "" {
					require.NoError(t, os.Mkdir(filepath.Join(home, ".alpacon"), 0o700))
					require.NoError(t, os.WriteFile(filepath.Join(home, ".alpacon", "config.json"), []byte(tt.config), 0o600))
				}

				stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, append(agentHelperEnv(), "HOME="+home), outputFormat,
					"http://127.0.0.1:1", createArgs("--use")...)

				assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "stderr: %s", stderr)
				assert.Empty(t, stdout)
				if outputFormat == utils.OutputFormatTable {
					assert.Contains(t, stderr, tt.wantMessage)
					if tt.noBugFooter {
						assert.NotContains(t, stderr, "For issues, check the latest version")
					}
					return
				}
				var got errorEnvelope
				require.NoError(t, json.Unmarshal([]byte(stderr), &got), "stderr: %s", stderr)
				assert.Equal(t, utils.ExitCodeGeneralError, got.ExitCode)
				assert.Equal(t, tt.wantCode, got.ErrorCode)
				assert.Equal(t, opCreate, got.Context.Operation)
				assert.Contains(t, got.Message, tt.wantMessage)
			})
		}
	}
}

func TestWorkSessionCreateUseReportsABadScopeBeforeAMissingLogin(t *testing.T) {
	t.Parallel()

	stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, append(agentHelperEnv(), "HOME="+t.TempDir()), utils.OutputFormatJSON,
		"http://127.0.0.1:1", createArgs("--use", "--scope", "bogus")...)

	assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
	assert.Empty(t, stdout)
	var got errorEnvelope
	require.NoError(t, json.Unmarshal([]byte(stderr), &got), "stderr: %s", stderr)
	assert.Equal(t, utils.ErrorCodeUsage, got.ErrorCode)
	assert.Contains(t, got.Message, "Invalid --scope: bogus")
}

func TestWorkSessionCreateUseOnAPendingSessionReportsPendingApproval(t *testing.T) {
	t.Parallel()
	for _, outputFormat := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(outputFormat, func(t *testing.T) {
			t.Parallel()
			ts := newCreateServerAnswering(t, "pending", "pending")

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), outputFormat, ts.URL,
				createArgs("--use")...)

			require.Equal(t, utils.ExitCodePendingApproval, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
			if outputFormat == utils.OutputFormatTable {
				assert.Empty(t, stdout)
				assert.Contains(t, stderr, "--use requires the session to be active, and work session ses-x is pending. A human must approve it in the Alpacon console (web).")
				assert.Contains(t, stderr, "alpacon work-session use ses-x")
				assert.Contains(t, stderr, "alpacon work-session update ses-x --server <server,...>")
				assert.Equal(t, 1, strings.Count(stderr, "--wait-approval"), "stderr: %s", stderr)
				return
			}
			var envelope struct {
				Status    string `json:"status"`
				ExitCode  int    `json:"exit_code"`
				Message   string `json:"message"`
				RequestID string `json:"request_id"`
				Context   struct {
					RequestID string `json:"request_id"`
				} `json:"context"`
				NextActions []utils.NextAction `json:"next_actions"`
			}
			require.NoError(t, json.Unmarshal([]byte(stdout), &envelope), "stdout: %s", stdout)
			assert.Empty(t, stderr)
			assert.Equal(t, utils.PendingApprovalStatus, envelope.Status)
			assert.Equal(t, utils.ExitCodePendingApproval, envelope.ExitCode)
			assert.Equal(t, "apr-1", envelope.RequestID)
			assert.Equal(t, "apr-1", envelope.Context.RequestID)
			assert.Equal(t, "--use requires the session to be active, and work session ses-x is pending. A human must approve it in the Alpacon console (web).", envelope.Message)
			commands := make([]string, 0, len(envelope.NextActions))
			for _, action := range envelope.NextActions {
				commands = append(commands, action.Command)
			}
			assert.Equal(t, []string{"alpacon work-session use ses-x", "", "alpacon work-session update ses-x --server <server,...>"}, commands)
		})
	}
}

// The session outlives a refusal, so every path that reports one names the session and only the
// follow-ups it can still take: not 'work-session use' (see TestRunUseSessionRefusesASettledSession).
func TestWorkSessionCreateSettledRefusalNamesTheSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		createStatus string
		pollStatus   string
		args         []string
	}{
		{name: "wait on a pending create", createStatus: "pending", pollStatus: "rejected", args: createArgs("--wait")},
		{name: "use on a rejected create", createStatus: "rejected", pollStatus: "rejected", args: createArgs("--use")},
		{name: "wait and use on a rejected create", createStatus: "rejected", pollStatus: "rejected", args: createArgs("--wait", "--use")},
	}
	for _, tt := range tests {
		for _, outputFormat := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
			t.Run(tt.name+" "+outputFormat, func(t *testing.T) {
				t.Parallel()
				ts := newCreateServerAnswering(t, tt.createStatus, tt.pollStatus)

				stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), outputFormat, ts.URL, tt.args...)

				assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "a refusal is settled, not pending; stderr: %s", stderr)
				assert.Empty(t, stdout)
				if outputFormat == utils.OutputFormatJSON {
					assert.JSONEq(t, `{
						"ok": false,
						"exit_code": 6,
						"error_code": "not_approved",
						"message": "work session was rejected (work session ses-x)",
						"context": {"operation": "create", "work_session_id": "ses-x"},
						"next_actions": [
							{"command": "alpacon work-session describe ses-x", "description": "why it settled"},
							{"command": "alpacon work-session complete ses-x"}
						]
					}`, stderr)
					return
				}
				assert.Contains(t, stderr, "work session was rejected (work session ses-x)")
				assert.Contains(t, stderr, "alpacon work-session describe ses-x")
				assert.Contains(t, stderr, "alpacon work-session complete ses-x")
				assert.NotContains(t, stderr, "work-session use")
				assert.NotContains(t, stderr, "work-session update")
				assert.NotContains(t, stderr, "report on https://github.com", "a reviewer saying no is not a bug to file")
			})
		}
	}
}

func TestWorkSessionCreateWithoutUseReportsASettledSessionAsCreated(t *testing.T) {
	t.Parallel()
	ts := newCreateServerAnswering(t, "rejected", "rejected")

	stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), utils.OutputFormatTable, ts.URL, createArgs()...)

	assert.Equal(t, 0, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Work session created: ses-x (status: rejected")
	assert.NotContains(t, stderr, "work session was rejected")
}

func TestWorkSessionCreateUseOnAnUnknownStatusExitsGeneralError(t *testing.T) {
	t.Parallel()
	for _, outputFormat := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(outputFormat, func(t *testing.T) {
			t.Parallel()
			ts := newCreateServerAnswering(t, "paused", "paused")

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), outputFormat, ts.URL,
				createArgs("--use")...)

			require.Equal(t, utils.ExitCodeGeneralError, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
			assert.Empty(t, stdout)
			const message = "--use requires the session to be active, but work session ses-x is paused."
			if outputFormat == utils.OutputFormatTable {
				assert.Contains(t, stderr, message)
				return
			}
			var got errorEnvelope
			require.NoError(t, json.Unmarshal([]byte(stderr), &got), "stderr: %s", stderr)
			assert.Equal(t, utils.ExitCodeGeneralError, got.ExitCode)
			assert.Equal(t, utils.ErrorCodeCLI, got.ErrorCode)
			assert.Equal(t, opCreate, got.Context.Operation)
			assert.Equal(t, message, got.Message)
		})
	}
}

func TestWaitExitErrorReturnsTheExitInsteadOfTakingIt(t *testing.T) {
	session := &wsapi.WorkSession{ID: "550e8400-e29b-41d4-a716-446655440000"}
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantText string
	}{
		{name: "settled", err: &terminalWaitError{message: "work session was rejected", sessionID: session.ID}, wantCode: utils.ExitCodeNotApproved, wantText: "work session was rejected"},
		{name: "still_pending", err: &pendingWaitError{message: "timed out waiting for approval after 1s"}, wantCode: utils.ExitCodePendingApproval, wantText: "The outcome is still open"},
		{name: "polling_failed", err: fmt.Errorf("polling failed: %w", os.ErrDeadlineExceeded), wantCode: utils.ExitCodeGeneralError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			_, stderr := testutil.CaptureOutput(t, func() {
				err = WaitExitError(opCreate, session, utils.NextAction{}, tc.err)
			})

			var exitErr *utils.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, tc.wantCode, exitErr.Code)
			if tc.wantText != "" {
				assert.Contains(t, stderr, tc.wantText) // printed here, so the exit itself stays silent
			}
		})
	}
}
