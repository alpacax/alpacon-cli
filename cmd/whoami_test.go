package cmd

import (
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
		{"auth0 token", "", "some-access-token", "Auth0 (browser)"},
		{"api token", "some-token", "", "API token"},
		{"both tokens prefers auth0", "some-token", "some-access-token", "Auth0 (browser)"},
		{"no tokens", "", "", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{Token: tt.token, AccessToken: tt.access}
			assert.Equal(t, tt.expected, getAuthMethod(cfg))
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
