package config

import (
	"encoding/json"
	"errors"
	"io/fs"
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
	err = SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2", false)
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

func TestSwitchWorkspace_RecordsKubernetesSurface(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "", "", "access-token", "", "alpacon.io", 3600, false))
	require.NoError(t, SetKubernetesSurface("https://ws1.us1.alpacon.io", true))

	// A switch writes the new workspace's answer in the same save.
	require.NoError(t, SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2", false))
	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ws2", cfg.WorkspaceName)
	assert.False(t, cfg.KubernetesSurface)

	require.NoError(t, SwitchWorkspace("https://ws3.us1.alpacon.io", "ws3", true))
	cfg, err = LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ws3", cfg.WorkspaceName)
	assert.True(t, cfg.KubernetesSurface)

	// A failed switch restores the original workspace's answer in the same write.
	require.NoError(t, RestoreWorkspace("https://ws1.us1.alpacon.io", "ws1", "", true))
	cfg, err = LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ws1", cfg.WorkspaceName)
	assert.True(t, cfg.KubernetesSurface)
	assert.Equal(t, "access-token", cfg.AccessToken)
}

func TestSetKubernetesSurface_PreservesOtherFields(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "", "", "access-token", "refresh-token", "alpacon.io", 3600, false))
	require.NoError(t, SetActiveWorkSession("ses-1"))

	require.NoError(t, SetKubernetesSurface("https://ws1.us1.alpacon.io", true))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.KubernetesSurface)
	assert.Equal(t, "https://ws1.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "access-token", cfg.AccessToken)
	assert.Equal(t, "refresh-token", cfg.RefreshToken)
	assert.Equal(t, "alpacon.io", cfg.BaseDomain)
	assert.Equal(t, map[string]string{"ws1": "ses-1"}, cfg.ActiveWorkSessions)

	require.NoError(t, SetKubernetesSurface("https://ws1.us1.alpacon.io", false))

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(homeDir, ConfigFileDir, ConfigFileName))
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.NotContains(t, raw, "kubernetes_surface")
	assert.Equal(t, "access-token", raw["access_token"])
}

func TestSetKubernetesSurface_NoExistingConfig(t *testing.T) {
	setupTestConfig(t)

	err := SetKubernetesSurface("https://ws1.us1.alpacon.io", true)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSetKubernetesSurface_RefusesAnotherWorkspace(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "token", "", "", "", "", 0, false))
	require.NoError(t, SetKubernetesSurface("https://ws1.us1.alpacon.io", true))

	// Another shell switched to ws2 while ws1's answer was being fetched.
	require.NoError(t, SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2", false))

	err := SetKubernetesSurface("https://ws1.us1.alpacon.io", true)
	require.ErrorIs(t, err, ErrWorkspaceChanged)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://ws2.us1.alpacon.io", cfg.WorkspaceURL)
	assert.False(t, cfg.KubernetesSurface)
}

func TestCreateConfig_ResetsKubernetesSurface(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "token", "", "", "", "", 0, false))
	require.NoError(t, SetKubernetesSurface("https://ws1.us1.alpacon.io", true))

	// A fresh login rebuilds the file; the capability belongs to the old
	// login until the new one records its own answer.
	require.NoError(t, CreateConfig("https://ws2.us1.alpacon.io", "ws2", "token", "", "", "", "", 0, false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.False(t, cfg.KubernetesSurface)
}

func TestSwitchWorkspace_NoExistingConfig(t *testing.T) {
	setupTestConfig(t)

	err := SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2", false)
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
	assert.False(t, cfg.KubernetesSurface)
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

	require.NoError(t, SwitchWorkspace("https://ws-b.example.com", "ws-b", false))
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got, "switching workspace should yield empty active session for new workspace")

	require.NoError(t, SetActiveWorkSession("uuid-B"))

	require.NoError(t, SwitchWorkspace("https://ws-a.example.com", "ws-a", false))
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

func TestGetActiveWorkSessionReportsNoSessionWhenNoConfigExists(t *testing.T) {
	setupTestConfig(t)
	// LoadConfig wraps the missing-file error with %w, and os.IsNotExist unwraps
	// only the error types the os package defines—so the promise this makes had
	// been reporting the absence of a config as a failure to read one.
	uuid, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, uuid)
}

func TestWorkspaceIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"schema name wins over a renamed host label", Config{WorkspaceName: "new-slug", SchemaName: "frozen"}, "frozen"},
		{"legacy config falls back to the host label", Config{WorkspaceName: "my-workspace"}, "my-workspace"},
		{"empty config has no identity", Config{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.cfg.WorkspaceIdentity())
		})
	}
}

func TestSetSchemaName_KeepsEverythingElse(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "access", "refresh", "alpacon.io", 3600, false))

	require.NoError(t, SetSchemaName("frozen"))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "frozen", cfg.SchemaName)
	assert.Equal(t, "new-slug", cfg.WorkspaceName, "the host label stays as the URL's slug")
	assert.Equal(t, "https://new-slug.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "access", cfg.AccessToken)
}

func TestSetSchemaName_EmptyKeepsTheStoredValue(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://ws.us1.alpacon.io", "ws", "", "", "access", "", "alpacon.io", 0, false))
	require.NoError(t, SetSchemaName("frozen"))

	// An older server omits schema_name from the env response.
	require.NoError(t, SetSchemaName(""))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "frozen", cfg.SchemaName)
}

func TestCreateConfig_ResetsSchemaNameUntilLoginRecordsIt(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://a.us1.alpacon.io", "a", "", "", "", "", "", 0, false))
	require.NoError(t, SetSchemaName("schema-a"))

	require.NoError(t, CreateConfig("https://b.us1.alpacon.io", "b", "", "", "", "", "", 0, false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "b", cfg.WorkspaceIdentity(), "one workspace's schema_name must not outlive its login")
}

func TestSwitchWorkspace_ReplacesSchemaName(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "access", "", "alpacon.io", 0, false))
	require.NoError(t, SetSchemaName("frozen"))

	require.NoError(t, SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2", false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ws2", cfg.WorkspaceIdentity())
}

func TestActiveWorkSession_KeyedBySchemaName(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "", "", "", 0, false))
	require.NoError(t, SetSchemaName("frozen"))

	require.NoError(t, SetActiveWorkSession("uuid-1"))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"frozen": "uuid-1"}, cfg.ActiveWorkSessions)
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-1", got)
}

func TestGetActiveWorkSession_FindsSessionStoredUnderTheHostLabel(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-old"))
	require.NoError(t, SetSchemaName("frozen"))

	got, err := GetActiveWorkSession()

	require.NoError(t, err)
	assert.Equal(t, "uuid-old", got, "a session saved before the upgrade stays reachable")
}

func TestRestoreWorkspace_PutsBackHostLabelAndSchemaName(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "access", "", "alpacon.io", 0, false))
	require.NoError(t, SetSchemaName("frozen"))
	require.NoError(t, SetActiveWorkSession("uuid-1"))
	require.NoError(t, SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2", false))

	require.NoError(t, RestoreWorkspace("https://new-slug.us1.alpacon.io", "new-slug", "frozen", false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://new-slug.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "new-slug", cfg.WorkspaceName)
	assert.Equal(t, "frozen", cfg.SchemaName)
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-1", got)
}

func TestRestoreWorkspace_LegacyConfigStaysWithoutSchemaName(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "", "", "access", "", "alpacon.io", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-old"))
	require.NoError(t, SwitchWorkspace("https://ws2.us1.alpacon.io", "ws2", false))

	require.NoError(t, RestoreWorkspace("https://ws1.us1.alpacon.io", "ws1", "", false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.SchemaName)
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-old", got)
}

func TestUnsetActiveWorkSession_ClearsTheLegacyHostLabelKeyToo(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-old"))
	require.NoError(t, SetSchemaName("frozen"))

	// Only the legacy key exists: the unset must still reach it.
	require.NoError(t, SetActiveWorkSession(""))
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestUnsetActiveWorkSession_DoesNotRevealTheLegacySession(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-old"))
	require.NoError(t, SetSchemaName("frozen"))
	require.NoError(t, SetActiveWorkSession("uuid-new"))

	require.NoError(t, SetActiveWorkSession(""))

	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got, "unsetting the newer session must not bring the older one back")
}
