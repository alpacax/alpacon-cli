package cmd

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
)

func TestApplyApplicationPrincipal(t *testing.T) {
	who := &auth.WhoamiResponse{
		PrincipalType: "application",
		Auth:          auth.WhoamiAuth{Scopes: []string{"server:read", "command:create"}},
		Application:   &auth.WhoamiApplication{Name: "ci-runner", ServiceType: "ci_cd", Username: "svc-ci"},
	}

	out := applyApplicationPrincipal(whoamiOutput{}, who)

	assert.Equal(t, "application", out.PrincipalType)
	assert.Equal(t, "ci-runner", out.ApplicationName)
	assert.Equal(t, "ci_cd", out.ServiceType)
	assert.Equal(t, "svc-ci", out.ServiceAccount)
	assert.Equal(t, []string{"server:read", "command:create"}, out.Scopes)
}

func TestApplicationFieldsOmittedForUserPrincipal(t *testing.T) {
	// A user principal's JSON must not carry application keys (output unchanged).
	body, err := json.Marshal(whoamiOutput{Username: "alice", AuthMethod: "Browser login"})
	assert.NoError(t, err)

	var got map[string]json.RawMessage
	assert.NoError(t, json.Unmarshal(body, &got))
	for _, k := range []string{"principal_type", "application_name", "service_type", "service_account", "scopes"} {
		_, present := got[k]
		assert.Falsef(t, present, "key %q must be absent for a user principal", k)
	}
}

func TestGetAuthMethod(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		access   string
		expected string
	}{
		{"browser login", "", "some-access-token", "Browser login"},
		{"service token", "alpst-abc", "", "Service token"},
		{"token", "some-token", "", "Token"},
		{"both tokens prefers browser", "some-token", "some-access-token", "Browser login"},
		{"no tokens", "", "", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{Token: tt.token, AccessToken: tt.access}
			assert.Equal(t, tt.expected, config.GetAuthMethod(cfg))
		})
	}
}

func TestGetAuthClassification(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config.Config
		expected string
	}{
		{"browser login", config.Config{AccessToken: "some-access-token"}, "browser_login"},
		{"service token", config.Config{Token: "alpst-x"}, "service_token"},
		{"token", config.Config{Token: "some-token"}, "token"},
		{"both tokens prefers browser", config.Config{Token: "some-token", AccessToken: "some-access-token"}, "browser_login"},
		{"no tokens", config.Config{}, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, getAuthClassification(tt.cfg))
		})
	}
}

func TestAuthClassificationFromMethod(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		expected string
	}{
		{"browser login", "Browser login", "browser_login"},
		{"service token", "Service token", "service_token"},
		{"token", "Token", "token"},
		{"unknown", "something else", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, authClassificationFromMethod(tt.method))
		})
	}
}

func TestGetRole(t *testing.T) {
	tests := []struct {
		name        string
		isStaff     bool
		isSuperuser bool
		expected    string
	}{
		{"superuser", true, true, "superuser"},
		{"staff only", true, false, "staff"},
		{"regular user", false, false, "user"},
		{"superuser without staff", false, true, "superuser"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, getRole(tt.isStaff, tt.isSuperuser))
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{"seconds only", 45 * time.Second, "45s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"just under a minute", 59 * time.Second, "59s"},
		{"one minute", 60 * time.Second, "1m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatDuration(tt.d))
		})
	}
}

func TestFormatExpiresHuman(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"empty string", "", ""},
		{"invalid format", "not-a-date", "not-a-date"},
		{"expired token", time.Now().Add(-1 * time.Hour).Format(time.RFC3339), "expired"},
		{"future token with days", time.Now().Add(48 * time.Hour).Format(time.RFC3339), "remaining"},
		{"future token under 1 hour", time.Now().Add(30 * time.Minute).Format(time.RFC3339), "remaining"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatExpiresHuman(tt.input)
			if tt.contains == "" {
				assert.Empty(t, result)
			} else {
				assert.Contains(t, result, tt.contains)
			}
		})
	}
}

func TestFormatGroups(t *testing.T) {
	tests := []struct {
		name     string
		groups   []iam.GroupMembership
		expected string
	}{
		{"empty", nil, ""},
		{"single group", []iam.GroupMembership{{Name: "infra", Role: "owner"}}, "infra (owner)"},
		{"multiple groups", []iam.GroupMembership{
			{Name: "infra", Role: "owner"},
			{Name: "backend", Role: "member"},
		}, "infra (owner), backend (member)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatGroups(tt.groups))
		})
	}
}

func TestFormatWSRequired(t *testing.T) {
	tests := []struct {
		name     string
		required bool
		active   *activeWorkSessionSummary
		expected string
	}{
		{
			name:     "not required",
			required: false,
			active:   nil,
			expected: "no",
		},
		{
			name:     "required, no active session",
			required: true,
			active:   nil,
			expected: "yes—run 'alpacon work-session list' to see available sessions",
		},
		{
			name:     "required, with active session",
			required: true,
			active:   &activeWorkSessionSummary{ID: "ws-123", Status: "active"},
			expected: "yes—active session ws-123 (active)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatWSRequired(tt.required, tt.active))
		})
	}
}

func TestPrintWhoamiJSON_PreflightFields(t *testing.T) {
	tests := []struct {
		name                string
		worksessionRequired bool
		activeWorksession   *activeWorkSessionSummary
		wantActiveRaw       string
	}{
		{
			name:                "required=true, no active session",
			worksessionRequired: true,
			activeWorksession:   nil,
			wantActiveRaw:       "null",
		},
		{
			name:                "required=false, no active session",
			worksessionRequired: false,
			activeWorksession:   nil,
			wantActiveRaw:       "null",
		},
		{
			name:                "required=true, with active session",
			worksessionRequired: true,
			activeWorksession: &activeWorkSessionSummary{
				ID:      "ws-123",
				Status:  "active",
				Scopes:  []string{"websh"},
				Servers: []string{"srv-1"},
			},
			wantActiveRaw: `{"id":"ws-123","status":"active","scopes":["websh"],"servers":["srv-1"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := whoamiOutput{
				AuthMethod:          "Browser login",
				WorksessionRequired: tt.worksessionRequired,
				ActiveWorksession:   tt.activeWorksession,
			}
			body, err := json.Marshal(output)
			assert.NoError(t, err)

			var got map[string]json.RawMessage
			assert.NoError(t, json.Unmarshal(body, &got))

			// Contract: both keys must always be present in the JSON output,
			// so callers can rely on them without checking for missing fields.
			_, hasRequired := got["work_session_required"]
			_, hasRequiredForAccess := got["worksession_required_for_access"]
			_, hasActive := got["active_work_session"]
			_, hasActiveCanonical := got["active_worksession"]
			_, hasAuthClassification := got["auth_classification"]
			assert.True(t, hasRequired, "work_session_required key must always be present")
			assert.True(t, hasRequiredForAccess, "worksession_required_for_access key must always be present")
			assert.True(t, hasActive, "active_work_session key must always be present")
			assert.True(t, hasActiveCanonical, "active_worksession key must always be present")
			assert.True(t, hasAuthClassification, "auth_classification key must always be present")

			assert.JSONEq(t, strconv.FormatBool(tt.worksessionRequired), string(got["work_session_required"]))
			assert.JSONEq(t, strconv.FormatBool(tt.worksessionRequired), string(got["worksession_required_for_access"]))
			assert.JSONEq(t, tt.wantActiveRaw, string(got["active_work_session"]))
			assert.JSONEq(t, tt.wantActiveRaw, string(got["active_worksession"]))
			assert.JSONEq(t, `"browser_login"`, string(got["auth_classification"]))
		})
	}
}

func TestIsWorksessionRequired(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "AccessToken → required",
			cfg:  config.Config{AccessToken: "eyJ..."},
			want: true,
		},
		{
			name: "Token only → not required",
			cfg:  config.Config{Token: "abc123"},
			want: false,
		},
		{
			name: "no tokens → not required",
			cfg:  config.Config{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isWorksessionRequired(tt.cfg))
		})
	}
}
