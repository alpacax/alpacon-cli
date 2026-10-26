package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTarget = SignerTarget{URL: "https://alpacon.ai", Audience: "https://alpacon.ai/"}

const (
	signerTestWorkspace      = "https://ws1.us1.alpacon.io"
	signerTestOtherWorkspace = "https://ws2.us1.alpacon.io"
)

func TestSignerToken(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	valid := func() Config {
		return Config{WorkspaceURL: signerTestWorkspace, Signer: &SignerGrant{
			AccessToken:  "signer",
			ExpiresAt:    now.Add(time.Hour).Format(time.RFC3339),
			WorkspaceURL: signerTestWorkspace,
			Target:       testTarget,
		}}
	}

	tests := []struct {
		name   string
		mutate func(*Config)
		want   bool
	}{
		{name: "valid", mutate: func(*Config) {}, want: true},
		{name: "none cached", mutate: func(c *Config) { c.Signer = nil }},
		{name: "empty token", mutate: func(c *Config) { c.Signer.AccessToken = "" }},
		{name: "authorized for another workspace", mutate: func(c *Config) { c.WorkspaceURL = signerTestOtherWorkspace }},
		{name: "issued for another signer URL", mutate: func(c *Config) { c.Signer.Target.URL = "https://signer.example.com" }},
		{name: "issued for another audience", mutate: func(c *Config) { c.Signer.Target.Audience = "https://signer.example.com/" }},
		{name: "expired", mutate: func(c *Config) { c.Signer.ExpiresAt = now.Add(-time.Second).Format(time.RFC3339) }},
		{name: "inside the expiry margin", mutate: func(c *Config) { c.Signer.ExpiresAt = now.Add(30 * time.Second).Format(time.RFC3339) }},
		{name: "unreadable expiry", mutate: func(c *Config) { c.Signer.ExpiresAt = "tomorrow" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid()
			tt.mutate(&cfg)
			token, ok := cfg.SignerToken(now, testTarget)
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

	require.NoError(t, SaveSignerToken(loadedConfig(t), testTarget, "signer", 86400))

	cfg := loadedConfig(t)
	token, ok := cfg.SignerToken(time.Now(), testTarget)
	require.True(t, ok, "a token cached a moment ago must be usable")
	assert.Equal(t, "signer", token)
	assert.Equal(t, signerTestWorkspace, cfg.Signer.WorkspaceURL)
	assert.Equal(t, testTarget, cfg.Signer.Target)
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
		{name: "a logout", wantErr: fs.ErrNotExist, move: DeleteConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
			started := loadedConfig(t)
			require.NoError(t, tt.move(started))

			err := SaveSignerToken(started, testTarget, "signer", 86400)

			require.ErrorIs(t, err, tt.wantErr)
			if cfg, loadErr := LoadConfig(); loadErr == nil {
				assert.Nil(t, cfg.Signer)
			}
		})
	}
}

func TestClearSignerToken_LeavesAnotherShellsTokenAlone(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
	require.NoError(t, SaveSignerToken(loadedConfig(t), testTarget, "newer", 86400))

	require.NoError(t, ClearSignerToken("refused"))
	assert.Equal(t, "newer", loadedConfig(t).Signer.AccessToken)

	require.NoError(t, ClearSignerToken("newer"))
	cfg := loadedConfig(t)
	assert.Nil(t, cfg.Signer)
}

// The signer token has no refresh token to revoke, so nothing but the login's
// own lifecycle removes it: a new login rewrites the file and a logout deletes it.
func TestSignerToken_EndsWithTheLogin(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
	require.NoError(t, SaveSignerToken(loadedConfig(t), testTarget, "signer", 86400))

	require.NoError(t, CreateConfig(signerTestWorkspace, "ws1", "", "", "access-2", "refresh-2", "alpacon.io", 3600, false))
	assert.Nil(t, loadedConfig(t).Signer, "a new login starts without the old signer token")

	require.NoError(t, SaveSignerToken(loadedConfig(t), testTarget, "signer-2", 86400))
	require.NoError(t, DeleteConfig(loadedConfig(t)))
	_, err := LoadConfig()
	assert.Error(t, err, "logout removes the file that held the signer token")
}

// A token cached in the flat layout an earlier build of this branch wrote is
// ignored, and the next acl command simply authorizes again.
func TestSignerToken_IgnoresTheOldFlatCache(t *testing.T) {
	setupTestConfig(t)
	dir := filepath.Join(os.Getenv("HOME"), ConfigFileDir)
	require.NoError(t, os.MkdirAll(dir, 0700))
	old := `{"workspace_url": "` + signerTestWorkspace + `", "workspace_name": "ws1", "access_token": "access", "refresh_token": "refresh",
		"signer_access_token": "old", "signer_access_token_expires_at": "2099-01-01T00:00:00Z", "signer_workspace_url": "` + signerTestWorkspace + `"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(old), 0600))

	cfg := loadedConfig(t)
	_, ok := cfg.SignerToken(time.Now(), testTarget)

	assert.False(t, ok)
	assert.Equal(t, "access", cfg.AccessToken, "the rest of the config still loads")
}
