package worksession

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
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
