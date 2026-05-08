package worksession

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"normal", "command,websh", []string{"command", "websh"}},
		{"whitespace around values", " command , websh ", []string{"command", "websh"}},
		{"trailing comma", "command,websh,", []string{"command", "websh"}},
		{"leading comma", ",command,websh", []string{"command", "websh"}},
		{"empty input", "", []string{}},
		{"single value", "command", []string{"command"}},
		{"only commas", ",,,", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitCSV(tt.input))
		})
	}
}
