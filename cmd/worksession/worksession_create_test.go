package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExpiryFlag_ExpiresIn(t *testing.T) {
	before := time.Now()
	result, err := parseExpiryFlag("2h", "")
	after := time.Now()

	assert.NoError(t, err)
	parsed, parseErr := time.Parse(time.RFC3339, result)
	assert.NoError(t, parseErr)
	assert.True(t, parsed.After(before.Add(2*time.Hour-time.Second)))
	assert.True(t, parsed.Before(after.Add(2*time.Hour+time.Second)))
}

func TestParseExpiryFlag_ExpiresAt(t *testing.T) {
	ts := "2026-12-31T23:59:59Z"
	result, err := parseExpiryFlag("", ts)
	assert.NoError(t, err)
	assert.Equal(t, ts, result)
}

func TestParseExpiryFlag_BothProvided(t *testing.T) {
	_, err := parseExpiryFlag("2h", "2026-12-31T23:59:59Z")
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "mutually exclusive"))
}

func TestParseExpiryFlag_NeitherProvided(t *testing.T) {
	_, err := parseExpiryFlag("", "")
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "required"))
}

func TestParseExpiryFlag_InvalidDuration(t *testing.T) {
	_, err := parseExpiryFlag("2hours", "")
	assert.Error(t, err)
}

func TestParseExpiryFlag_ZeroDuration(t *testing.T) {
	_, err := parseExpiryFlag("0s", "")
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "positive duration"))
}

func TestParseExpiryFlag_NegativeDuration(t *testing.T) {
	_, err := parseExpiryFlag("-1h", "")
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "positive duration"))
}

func TestValidateAgentScopes_AgentWithWebsh(t *testing.T) {
	err := validateAgentScopes("agent", []string{"command", "websh"})
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "\"websh\" is not allowed"))
}

func TestValidateAgentScopes_AgentWithoutWebsh(t *testing.T) {
	err := validateAgentScopes("agent", []string{"command", "webftp"})
	assert.NoError(t, err)
}

func TestValidateAgentScopes_UserWithWebsh(t *testing.T) {
	err := validateAgentScopes("user", []string{"command", "websh"})
	assert.NoError(t, err)
}

func TestValidateScopeEnum(t *testing.T) {
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
				assert.Error(t, err)
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
	waitApproval = true

	_, stderr := captureWorkSessionCommandOutput(t, func() {
		workSessionCreateCmd.Run(workSessionCreateCmd, nil)
	})

	assert.Contains(t, stderr, "approved")
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
	waitApproval = true

	stdout, _ := captureWorkSessionCommandOutput(t, func() {
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
