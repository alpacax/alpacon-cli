package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	signerTestWorkspace      = "https://ws1.us1.alpacon.io"
	signerTestOtherWorkspace = "https://ws2.us1.alpacon.io"
)

func TestSignerToken(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	valid := Config{
		WorkspaceURL:               signerTestWorkspace,
		SignerAccessToken:          "signer",
		SignerAccessTokenExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
		SignerWorkspaceURL:         signerTestWorkspace,
	}

	tests := []struct {
		name   string
		mutate func(*Config)
		want   bool
	}{
		{name: "valid", mutate: func(*Config) {}, want: true},
		{name: "none cached", mutate: func(c *Config) { c.SignerAccessToken = "" }},
		{name: "authorized for another workspace", mutate: func(c *Config) { c.WorkspaceURL = signerTestOtherWorkspace }},
		{name: "expired", mutate: func(c *Config) { c.SignerAccessTokenExpiresAt = now.Add(-time.Second).Format(time.RFC3339) }},
		{name: "inside the expiry margin", mutate: func(c *Config) { c.SignerAccessTokenExpiresAt = now.Add(30 * time.Second).Format(time.RFC3339) }},
		{name: "unreadable expiry", mutate: func(c *Config) { c.SignerAccessTokenExpiresAt = "tomorrow" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			tt.mutate(&cfg)
			token, ok := cfg.SignerToken(now)
			assert.Equal(t, tt.want, ok)
			if tt.want {
				assert.Equal(t, "signer", token)
			} else {
				assert.Empty(t, token)
			}
		})
	}
}

func TestSaveSignerToken_CachesForTheLoginsWorkspace(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))

	require.NoError(t, SaveSignerToken(loadedConfig(t), "signer", 86400))

	cfg := loadedConfig(t)
	token, ok := cfg.SignerToken(time.Now())
	require.True(t, ok, "a token cached a moment ago must be usable")
	assert.Equal(t, "signer", token)
	assert.Equal(t, signerTestWorkspace, cfg.SignerWorkspaceURL)
	assert.Equal(t, "access", cfg.AccessToken, "the login's own token stays as it was")
}

func TestSaveSignerToken_WritesNothingOnceTheConfigMoved(t *testing.T) {
	tests := []struct {
		name    string
		move    func(started Config) error
		wantErr error
	}{
		{name: "another login", wantErr: ErrLoginChanged, move: func(Config) error {
			return CreateConfig(signerTestWorkspace, "ws1", "", "", "access-2", "refresh-2", "alpacon.io", 3600, false)
		}},
		{name: "a switch", wantErr: ErrWorkspaceChanged, move: func(started Config) error {
			return SwitchWorkspace(started, signerTestOtherWorkspace, "ws2", false)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
			started := loadedConfig(t)
			require.NoError(t, tt.move(started))

			err := SaveSignerToken(started, "signer", 86400)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Empty(t, loadedConfig(t).SignerAccessToken)
		})
	}
}

func TestClearSignerToken_LeavesAnotherShellsTokenAlone(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
	require.NoError(t, SaveSignerToken(loadedConfig(t), "newer", 86400))

	require.NoError(t, ClearSignerToken("refused"))
	assert.Equal(t, "newer", loadedConfig(t).SignerAccessToken)

	require.NoError(t, ClearSignerToken("newer"))
	cfg := loadedConfig(t)
	assert.Empty(t, cfg.SignerAccessToken)
	assert.Empty(t, cfg.SignerAccessTokenExpiresAt)
	assert.Empty(t, cfg.SignerWorkspaceURL)
}

// The signer token has no refresh token to revoke, so nothing but the login's
// own lifecycle removes it: a new login rewrites the file and a logout deletes it.
func TestSignerToken_EndsWithTheLogin(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
	require.NoError(t, SaveSignerToken(loadedConfig(t), "signer", 86400))

	require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access-2", "refresh-2", "alpacon.io", 3600, false))
	assert.Empty(t, loadedConfig(t).SignerAccessToken, "a new login starts without the old signer token")

	require.NoError(t, SaveSignerToken(loadedConfig(t), "signer-2", 86400))
	require.NoError(t, DeleteConfig(loadedConfig(t)))
	_, err := LoadConfig()
	assert.Error(t, err, "logout removes the file that held the signer token")
}
