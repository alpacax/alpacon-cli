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

func TestValidateAgentScopes_AgentWithWebsh(t *testing.T) {
	err := validateAgentScopes("agent", []string{"command", "websh"})
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "websh"))
}

func TestValidateAgentScopes_AgentWithoutWebsh(t *testing.T) {
	err := validateAgentScopes("agent", []string{"command", "webftp"})
	assert.NoError(t, err)
}

func TestValidateAgentScopes_UserWithWebsh(t *testing.T) {
	err := validateAgentScopes("user", []string{"command", "websh"})
	assert.NoError(t, err)
}
