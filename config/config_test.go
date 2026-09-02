package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestConfig overrides the home directory so tests write to a temp dir.
// t.Setenv automatically restores the original value when the test finishes.
func setupTestConfig(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
}

func TestIsMultiWorkspaceMode(t *testing.T) {
	t.Parallel()
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
	setupTestConfig(t)

	err := CreateConfig(
		"https://myws.us1.alpacon.io", "myws",
		"", "", "access-token", "refresh-token",
		"alpacon.io", 3600, false,
	)
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "alpacon.io", cfg.BaseDomain)
	assert.Equal(t, "myws", cfg.WorkspaceName)
	assert.Equal(t, "https://myws.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "access-token", cfg.AccessToken)
	assert.Equal(t, "refresh-token", cfg.RefreshToken)
	assert.NotEmpty(t, cfg.AccessTokenExpiresAt)
}

func TestCreateConfig_WithoutBaseDomain(t *testing.T) {
	setupTestConfig(t)

	err := CreateConfig(
		"https://myws.us1.alpacon.io", "myws",
		"legacy-token", "2025-12-31T00:00:00Z", "", "",
		"", 0, false,
	)
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.BaseDomain)
	assert.Equal(t, "legacy-token", cfg.Token)
}

func TestCreateConfig_BaseDomainOmittedFromJSON(t *testing.T) {
	setupTestConfig(t)

	err := CreateConfig(
		"https://myws.us1.alpacon.io", "myws",
		"token", "", "", "",
		"", 0, false,
	)
	require.NoError(t, err)

	// Read raw JSON to verify omitempty works
	homeDir, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(homeDir, ConfigFileDir, ConfigFileName))
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)
	_, exists := raw["base_domain"]
	assert.False(t, exists, "base_domain should be omitted from JSON when empty")
}

func TestSwitchWorkspace(t *testing.T) {
	setupTestConfig(t)

	// Create initial config
	err := CreateConfig(
		"https://ws1.us1.alpacon.io", "ws1",
		"", "", "access-token", "refresh-token",
		"alpacon.io", 3600, false,
	)
	require.NoError(t, err)

	// Switch workspace
	err = SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2")
	require.NoError(t, err)

	// Verify only URL and name changed
	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://ws2.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "ws2", cfg.WorkspaceName)
	assert.Equal(t, "alpacon.io", cfg.BaseDomain, "BaseDomain should be preserved")
	assert.Equal(t, "access-token", cfg.AccessToken, "AccessToken should be preserved")
	assert.Equal(t, "refresh-token", cfg.RefreshToken, "RefreshToken should be preserved")
}

func TestSwitchWorkspace_NoExistingConfig(t *testing.T) {
	setupTestConfig(t)

	err := SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2")
	assert.Error(t, err)
}

func TestLoadConfig_LegacyWithoutActiveWorkSessions(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfgDir := filepath.Join(tmpHome, ConfigFileDir)
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	legacy := `{"workspace_url":"https://ws.example.com","workspace_name":"ws-a"}`
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, ConfigFileName), []byte(legacy), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.ActiveWorkSessions)
}

func TestActiveWorkSession_RoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	require.NoError(t, CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))

	require.NoError(t, SetActiveWorkSession("uuid-1"))
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-1", got)
}

func TestActiveWorkSession_UnsetRemovesKey(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	require.NoError(t, CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-1"))
	require.NoError(t, SetActiveWorkSession(""))

	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	_, exists := cfg.ActiveWorkSessions["ws-a"]
	assert.False(t, exists, "key should be removed from map on unset")
}

func TestActiveWorkSession_PerWorkspaceIsolation(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	require.NoError(t, CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-A"))

	require.NoError(t, SwitchWorkspace("https://ws-b.example.com", "ws-b"))
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got, "switching workspace should yield empty active session for new workspace")

	require.NoError(t, SetActiveWorkSession("uuid-B"))

	require.NoError(t, SwitchWorkspace("https://ws-a.example.com", "ws-a"))
	got, err = GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-A", got, "switching back should restore original active session")
}

func TestIsServiceToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"service token", "alpst-abc123", true},
		{"personal api token", "alpat-abc123", false},
		{"empty", "", false},
		{"leading whitespace", "  alpst-x", true},
		{"custom prefix not recognized", "custom-abc123", false}, // documented limitation
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsServiceToken(tt.token))
		})
	}
}

func TestGetAuthMethod(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cfg      Config
		expected string
	}{
		{
			name:     "access token present → Browser login",
			cfg:      Config{AccessToken: "eyJ..."},
			expected: "Browser login",
		},
		{
			name:     "service token → Service token",
			cfg:      Config{Token: "alpst-abc"},
			expected: "Service token",
		},
		{
			name:     "token only → Token",
			cfg:      Config{Token: "abc123"},
			expected: "Token",
		},
		{
			name:     "both tokens → AccessToken wins",
			cfg:      Config{AccessToken: "eyJ...", Token: "abc123"},
			expected: "Browser login",
		},
		{
			name:     "access token wins over service token",
			cfg:      Config{AccessToken: "eyJ...", Token: "alpst-abc"},
			expected: "Browser login",
		},
		{
			name:     "no tokens → unknown",
			cfg:      Config{},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetAuthMethod(tt.cfg))
		})
	}
}

// Another alpacon process can be saving a renewed token at the same moment, and
// whatever the two writers do to each other, a reader must never find a config
// it cannot parse—that failure reaches the user as a forced re-login.
func TestSaveConfig_ConcurrentWritesNeverLeaveAnUnreadableFile(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://alpacon.io", "alpacon", "", "", "eyJ", "refresh", "", 3600, false))

	configFile := filepath.Join(os.Getenv("HOME"), ConfigFileDir, ConfigFileName)

	var writers sync.WaitGroup
	// One slot per writer, each written only by its own goroutine, so collecting
	// the failures costs no synchronization. A writer that never lands leaves the
	// reader re-reading what CreateConfig wrote, which parses every time and would
	// let this test pass without ever testing a replacement.
	writeErrs := make([]error, 2)
	done := make(chan struct{})
	for w := range 2 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			// Varying lengths so a half-written file differs in size from the
			// one it replaced, which is what a reader would catch.
			for i := range 300 {
				if err := SaveRefreshedAuth0Token(strings.Repeat(string(rune('a'+w)), 1+i%400), 3600); err != nil {
					writeErrs[w] = err
					return
				}
			}
		}()
	}
	go func() {
		writers.Wait()
		close(done)
	}()

	reads := 0
	for {
		select {
		case <-done:
			require.NoError(t, errors.Join(writeErrs...))
			assert.Positive(t, reads, "the reader never got to run, so the test proved nothing")
			return
		default:
		}
		body, err := os.ReadFile(configFile)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		var cfg Config
		require.NoError(t, json.Unmarshal(body, &cfg), "read a config that does not parse: %q", body)
		reads++
	}
}
