package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// setupTestConfig overrides the home directory so tests write to a temp dir.
func setupTestConfig(t *testing.T) (cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	return func() {
		os.Setenv("HOME", origHome)
	}
}

func TestIsMultiWorkspaceMode(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected bool
	}{
		{
			name: "Auth0 login with base domain",
			config: Config{
				AccessToken: "some-token",
				BaseDomain:  "alpacon.io",
			},
			expected: true,
		},
		{
			name: "Auth0 login without base domain",
			config: Config{
				AccessToken: "some-token",
				BaseDomain:  "",
			},
			expected: false,
		},
		{
			name: "Legacy login with token only",
			config: Config{
				Token:      "legacy-token",
				BaseDomain: "",
			},
			expected: false,
		},
		{
			name: "API token login",
			config: Config{
				Token: "api-token",
			},
			expected: false,
		},
		{
			name:     "Empty config",
			config:   Config{},
			expected: false,
		},
		{
			name: "BaseDomain set but no access token",
			config: Config{
				BaseDomain: "alpacon.io",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsMultiWorkspaceMode()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreateConfig_WithBaseDomain(t *testing.T) {
	cleanup := setupTestConfig(t)
	defer cleanup()

	err := CreateConfig(
		"https://myws.ap1.alpacon.io", "myws",
		"", "", "access-token", "refresh-token",
		"alpacon.io", 3600, false,
	)
	assert.NoError(t, err)

	cfg, err := LoadConfig()
	assert.NoError(t, err)
	assert.Equal(t, "alpacon.io", cfg.BaseDomain)
	assert.Equal(t, "myws", cfg.WorkspaceName)
	assert.Equal(t, "https://myws.ap1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "access-token", cfg.AccessToken)
	assert.Equal(t, "refresh-token", cfg.RefreshToken)
	assert.NotEmpty(t, cfg.AccessTokenExpiresAt)
}

func TestCreateConfig_WithoutBaseDomain(t *testing.T) {
	cleanup := setupTestConfig(t)
	defer cleanup()

	err := CreateConfig(
		"https://myws.ap1.alpacon.io", "myws",
		"legacy-token", "2025-12-31T00:00:00Z", "", "",
		"", 0, false,
	)
	assert.NoError(t, err)

	cfg, err := LoadConfig()
	assert.NoError(t, err)
	assert.Equal(t, "", cfg.BaseDomain)
	assert.Equal(t, "legacy-token", cfg.Token)
}

func TestCreateConfig_BaseDomainOmittedFromJSON(t *testing.T) {
	cleanup := setupTestConfig(t)
	defer cleanup()

	err := CreateConfig(
		"https://myws.ap1.alpacon.io", "myws",
		"token", "", "", "",
		"", 0, false,
	)
	assert.NoError(t, err)

	// Read raw JSON to verify omitempty works
	homeDir, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(homeDir, ConfigFileDir, ConfigFileName))
	assert.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(data, &raw)
	assert.NoError(t, err)
	_, exists := raw["base_domain"]
	assert.False(t, exists, "base_domain should be omitted from JSON when empty")
}

func TestSwitchWorkspace(t *testing.T) {
	cleanup := setupTestConfig(t)
	defer cleanup()

	// Create initial config
	err := CreateConfig(
		"https://ws1.ap1.alpacon.io", "ws1",
		"", "", "access-token", "refresh-token",
		"alpacon.io", 3600, false,
	)
	assert.NoError(t, err)

	// Switch workspace
	err = SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2")
	assert.NoError(t, err)

	// Verify only URL and name changed
	cfg, err := LoadConfig()
	assert.NoError(t, err)
	assert.Equal(t, "https://ws2.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "ws2", cfg.WorkspaceName)
	assert.Equal(t, "alpacon.io", cfg.BaseDomain, "BaseDomain should be preserved")
	assert.Equal(t, "access-token", cfg.AccessToken, "AccessToken should be preserved")
	assert.Equal(t, "refresh-token", cfg.RefreshToken, "RefreshToken should be preserved")
}

func TestSwitchWorkspace_NoExistingConfig(t *testing.T) {
	cleanup := setupTestConfig(t)
	defer cleanup()

	err := SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2")
	assert.Error(t, err)
}
