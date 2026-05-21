package cmd

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
)

func TestGetAuthMethod(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		access   string
		expected string
	}{
		{"browser login", "", "some-access-token", "Browser login"},
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
			_, hasActive := got["active_work_session"]
			assert.True(t, hasRequired, "work_session_required key must always be present")
			assert.True(t, hasActive, "active_work_session key must always be present")

			assert.JSONEq(t, strconv.FormatBool(tt.worksessionRequired), string(got["work_session_required"]))
			assert.JSONEq(t, tt.wantActiveRaw, string(got["active_work_session"]))
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
