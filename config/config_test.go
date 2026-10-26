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

	"github.com/alpacax/alpacon-cli/utils"
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

func loadedConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadConfig()
	require.NoError(t, err)
	return cfg
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
	err = SwitchWorkspace(loadedConfig(t), "https://ws2.us1.alpacon.io", "ws2", false)
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
	require.NoError(t, SetWorkspaceEnv("https://ws1.us1.alpacon.io", "", true))
	orig := loadedConfig(t)

	// A switch writes the new workspace's answer in the same save.
	require.NoError(t, SwitchWorkspace(orig, "https://ws2.us1.alpacon.io", "ws2", false))
	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ws2", cfg.WorkspaceName)
	assert.False(t, cfg.KubernetesSurface)

	require.NoError(t, SwitchWorkspace(loadedConfig(t), "https://ws3.us1.alpacon.io", "ws3", true))
	cfg, err = LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ws3", cfg.WorkspaceName)
	assert.True(t, cfg.KubernetesSurface)

	// A failed switch restores the original workspace's answer in the same write.
	require.NoError(t, RestoreWorkspace("https://ws3.us1.alpacon.io", orig))
	cfg, err = LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ws1", cfg.WorkspaceName)
	assert.True(t, cfg.KubernetesSurface)
	assert.Equal(t, "access-token", cfg.AccessToken)
}

func TestSetWorkspaceEnv_KubernetesSurfacePreservesOtherFields(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "", "", "access-token", "refresh-token", "alpacon.io", 3600, false))
	require.NoError(t, SetActiveWorkSession("ses-1"))

	require.NoError(t, SetWorkspaceEnv("https://ws1.us1.alpacon.io", "", true))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.KubernetesSurface)
	assert.Equal(t, "https://ws1.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "access-token", cfg.AccessToken)
	assert.Equal(t, "refresh-token", cfg.RefreshToken)
	assert.Equal(t, "alpacon.io", cfg.BaseDomain)
	assert.Equal(t, map[string]string{"ws1": "ses-1"}, cfg.ActiveWorkSessions)

	require.NoError(t, SetWorkspaceEnv("https://ws1.us1.alpacon.io", "", false))

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(homeDir, ConfigFileDir, ConfigFileName))
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.NotContains(t, raw, "kubernetes_surface")
	assert.Equal(t, "access-token", raw["access_token"])
}

func TestSetWorkspaceEnv_NoExistingConfig(t *testing.T) {
	setupTestConfig(t)

	err := SetWorkspaceEnv("https://ws1.us1.alpacon.io", "", true)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSetWorkspaceEnv_RefusesAnotherWorkspace(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "token", "", "", "", "", 0, false))
	require.NoError(t, SetWorkspaceEnv("https://ws1.us1.alpacon.io", "", true))

	// Another shell switched to ws2 while ws1's answer was being fetched.
	require.NoError(t, SwitchWorkspace(loadedConfig(t), "https://ws2.us1.alpacon.io", "ws2", false))

	err := SetWorkspaceEnv("https://ws1.us1.alpacon.io", "frozen", true)
	require.ErrorIs(t, err, ErrWorkspaceChanged)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://ws2.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "ws2", cfg.SchemaName)
	assert.False(t, cfg.KubernetesSurface)
}

// Each of these writers decided what to write before a network call, during which another shell logged
// in, elsewhere or to the same workspace as another account; the write must leave that login alone.
func TestWritersDecidedBeforeAnotherLoginWriteNothing(t *testing.T) {
	const ws1, ws2, ws9 = "https://ws1.us1.alpacon.io", "https://ws2.us1.alpacon.io", "https://ws9.zz9.alpacon.io"
	tests := []struct {
		name         string
		anotherLogin string
		write        func(started Config) error
		wantErr      error
	}{
		{name: "switch after a login elsewhere", anotherLogin: ws9, wantErr: ErrWorkspaceChanged, write: func(started Config) error {
			return SwitchWorkspace(started, ws2, "ws2", true)
		}},
		{name: "switch after another account logged in to the same workspace", anotherLogin: ws1, wantErr: ErrLoginChanged, write: func(started Config) error {
			return SwitchWorkspace(started, ws2, "ws2", true)
		}},
		{name: "revert after a login elsewhere", anotherLogin: ws9, wantErr: ErrWorkspaceChanged, write: func(started Config) error {
			return RestoreWorkspace(ws2, started)
		}},
		{name: "revert after another account logged in to the workspace switched to", anotherLogin: ws2, wantErr: ErrLoginChanged, write: func(started Config) error {
			return RestoreWorkspace(ws2, started)
		}},
		{name: "workspace env fetched for the old workspace", anotherLogin: ws9, wantErr: ErrWorkspaceChanged, write: func(Config) error {
			return SetWorkspaceEnv(ws1, "frozen", true)
		}},
		{name: "token refreshed after a login elsewhere", anotherLogin: ws9, wantErr: ErrLoginChanged, write: func(started Config) error {
			return SaveRefreshedAuth0Token(started.RefreshToken, "access-from-ws1", 3600)
		}},
		{name: "token refreshed after another account logged in to the same workspace", anotherLogin: ws1, wantErr: ErrLoginChanged, write: func(started Config) error {
			return SaveRefreshedAuth0Token(started.RefreshToken, "access-from-ws1", 3600)
		}},
		{name: "logout after a login elsewhere", anotherLogin: ws9, wantErr: ErrLoginChanged, write: func(started Config) error {
			return DeleteConfig(started)
		}},
		{name: "logout after another account logged in to the same workspace", anotherLogin: ws1, wantErr: ErrLoginChanged, write: func(started Config) error {
			return DeleteConfig(started)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			require.NoError(t, CreateConfig(ws1, "ws1", "", "", "access-1", "refresh-1", "alpacon.io", 3600, false))
			started := loadedConfig(t)
			require.NoError(t, CreateConfig(tt.anotherLogin, "other", "", "", "access-9", "refresh-9", "alpacon.io", 3600, false))
			want := loadedConfig(t)

			err := tt.write(started)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, want, loadedConfig(t))
		})
	}
}

func TestWritersDecidedBeforeAnotherSwitchStillWrite(t *testing.T) {
	const ws1, ws2 = "https://ws1.us1.alpacon.io", "https://ws2.us1.alpacon.io"

	t.Run("token refresh", func(t *testing.T) {
		setupTestConfig(t)
		require.NoError(t, CreateConfig(ws1, "ws1", "", "", "access-1", "refresh-1", "alpacon.io", 3600, false))
		started := loadedConfig(t)
		require.NoError(t, SwitchWorkspace(started, ws2, "ws2", false))

		err := SaveRefreshedAuth0Token(started.RefreshToken, "access-2", 3600)

		require.NoError(t, err)
		cfg := loadedConfig(t)
		assert.Equal(t, ws2, cfg.WorkspaceURL)
		assert.Equal(t, "access-2", cfg.AccessToken)
	})

	t.Run("logout", func(t *testing.T) {
		setupTestConfig(t)
		require.NoError(t, CreateConfig(ws1, "ws1", "", "", "access-1", "refresh-1", "alpacon.io", 3600, false))
		started := loadedConfig(t)
		require.NoError(t, SwitchWorkspace(started, ws2, "ws2", false))

		err := DeleteConfig(started)

		require.NoError(t, err)
		_, err = LoadConfig()
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
}

func TestDeleteConfig_RemovesTheLoginItWasGiven(t *testing.T) {
	tests := []struct {
		name  string
		login func() error
	}{
		{name: "browser login", login: func() error {
			return CreateConfig("https://ws1.us1.alpacon.io", "ws1", "", "", "access-1", "refresh-1", "alpacon.io", 3600, false)
		}},
		{name: "API key login", login: func() error {
			return CreateConfig("https://alpacon.example.com", "", "key-1", "", "", "", "", 0, false)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			require.NoError(t, tt.login())

			err := DeleteConfig(loadedConfig(t))

			require.NoError(t, err)
			_, err = LoadConfig()
			require.ErrorIs(t, err, fs.ErrNotExist)
		})
	}
}

func TestDeleteConfig_KeepsAnotherAPIKeyLoginToTheSameServer(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://alpacon.example.com", "", "key-1", "", "", "", "", 0, false))
	started := loadedConfig(t)
	require.NoError(t, CreateConfig("https://alpacon.example.com", "", "key-2", "", "", "", "", 0, false))

	err := DeleteConfig(started)

	require.ErrorIs(t, err, ErrLoginChanged)
	assert.Equal(t, "key-2", loadedConfig(t).Token)
}

func TestDeleteConfig_AlreadyGoneIsNotAnError(t *testing.T) {
	setupTestConfig(t)

	err := DeleteConfig(Config{Token: "key-1"})

	require.NoError(t, err)
}

func TestCreateConfig_ResetsKubernetesSurface(t *testing.T) {
	setupTestConfig(t)

	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "token", "", "", "", "", 0, false))
	require.NoError(t, SetWorkspaceEnv("https://ws1.us1.alpacon.io", "", true))

	// A fresh login rebuilds the file; the capability belongs to the old
	// login until the new one records its own answer.
	require.NoError(t, CreateConfig("https://ws2.us1.alpacon.io", "ws2", "token", "", "", "", "", 0, false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.False(t, cfg.KubernetesSurface)
}

func TestSwitchWorkspace_NoExistingConfig(t *testing.T) {
	setupTestConfig(t)

	err := SwitchWorkspace(Config{WorkspaceURL: "https://ws1.us1.alpacon.io"}, "https://ws2.us1.alpacon.io", "ws2", false)
	require.ErrorIs(t, err, fs.ErrNotExist)
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

func TestLoadConfig_MissingFileIsNotLoggedIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := LoadConfig()

	require.ErrorIs(t, err, utils.ErrNotLoggedIn)
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.NotContains(t, err.Error(), utils.ErrNotLoggedIn.Error())
	assert.Equal(t, utils.ErrorCodeNotLoggedIn, utils.ClassifyError(err))
}

func TestLoadConfig_MalformedFileIsNotMissing(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	cfgDir := filepath.Join(tmpHome, ConfigFileDir)
	require.NoError(t, os.MkdirAll(cfgDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, ConfigFileName), []byte("{not json"), 0600))

	_, err := LoadConfig()

	require.Error(t, err)
	require.NotErrorIs(t, err, utils.ErrNotLoggedIn)
	assert.Equal(t, utils.ErrorCodeCLI, utils.ClassifyError(err))
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

	require.NoError(t, SwitchWorkspace(loadedConfig(t), "https://ws-b.example.com", "ws-b", false))
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got, "switching workspace should yield empty active session for new workspace")

	require.NoError(t, SetActiveWorkSession("uuid-B"))

	require.NoError(t, SwitchWorkspace(loadedConfig(t), "https://ws-a.example.com", "ws-a", false))
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

// Readers take no lock, so while writers replace the config one after another a reader
// must never find a config it cannot parse—that failure reaches the user as a forced re-login.
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
		writers.Go(func() {
			// Varying lengths so a half-written file differs in size from the
			// one it replaced, which is what a reader would catch.
			for i := range 300 {
				if err := SaveRefreshedAuth0Token("refresh", strings.Repeat(string(rune('a'+w)), 1+i%400), 3600); err != nil {
					writeErrs[w] = err
					return
				}
			}
		})
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

func TestSetWorkspaceEnv_SchemaNameKeepsEverythingElse(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "access", "refresh", "alpacon.io", 3600, false))

	require.NoError(t, SetWorkspaceEnv("https://new-slug.us1.alpacon.io", "frozen", false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "frozen", cfg.SchemaName)
	assert.Equal(t, "new-slug", cfg.WorkspaceName, "the host label stays as the URL's slug")
	assert.Equal(t, "https://new-slug.us1.alpacon.io", cfg.WorkspaceURL)
	assert.Equal(t, "access", cfg.AccessToken)
}

func TestSetWorkspaceEnv_EmptySchemaNameKeepsTheStoredValue(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://ws.us1.alpacon.io", "ws", "", "", "access", "", "alpacon.io", 0, false))
	require.NoError(t, SetWorkspaceEnv("https://ws.us1.alpacon.io", "frozen", false))

	// An older server omits schema_name from the env response.
	require.NoError(t, SetWorkspaceEnv("https://ws.us1.alpacon.io", "", false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "frozen", cfg.SchemaName)
}

func TestCreateConfig_ResetsSchemaNameUntilLoginRecordsIt(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://a.us1.alpacon.io", "a", "", "", "", "", "", 0, false))
	require.NoError(t, SetWorkspaceEnv("https://a.us1.alpacon.io", "schema-a", false))

	require.NoError(t, CreateConfig("https://b.us1.alpacon.io", "b", "", "", "", "", "", 0, false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "b", cfg.WorkspaceIdentity(), "one workspace's schema_name must not outlive its login")
}

func TestSwitchWorkspace_ReplacesSchemaName(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "access", "", "alpacon.io", 0, false))
	require.NoError(t, SetWorkspaceEnv("https://new-slug.us1.alpacon.io", "frozen", false))

	require.NoError(t, SwitchWorkspace(loadedConfig(t), "https://ws2.us1.alpacon.io", "ws2", false))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ws2", cfg.WorkspaceIdentity())
}

func TestActiveWorkSession_KeyedBySchemaName(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "", "", "", 0, false))
	require.NoError(t, SetWorkspaceEnv("https://new-slug.us1.alpacon.io", "frozen", false))

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
	require.NoError(t, SetWorkspaceEnv("https://new-slug.us1.alpacon.io", "frozen", false))

	got, err := GetActiveWorkSession()

	require.NoError(t, err)
	assert.Equal(t, "uuid-old", got, "a session saved before the upgrade stays reachable")
}

func TestRestoreWorkspace_PutsBackHostLabelAndSchemaName(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "access", "", "alpacon.io", 0, false))
	require.NoError(t, SetWorkspaceEnv("https://new-slug.us1.alpacon.io", "frozen", false))
	require.NoError(t, SetActiveWorkSession("uuid-1"))
	orig := loadedConfig(t)
	require.NoError(t, SwitchWorkspace(orig, "https://ws2.us1.alpacon.io", "ws2", false))

	require.NoError(t, RestoreWorkspace("https://ws2.us1.alpacon.io", orig))

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
	orig := loadedConfig(t)
	require.NoError(t, SwitchWorkspace(orig, "https://ws2.us1.alpacon.io", "ws2", false))

	require.NoError(t, RestoreWorkspace("https://ws2.us1.alpacon.io", orig))

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
	require.NoError(t, SetWorkspaceEnv("https://new-slug.us1.alpacon.io", "frozen", false))

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
	require.NoError(t, SetWorkspaceEnv("https://new-slug.us1.alpacon.io", "frozen", false))
	require.NoError(t, SetActiveWorkSession("uuid-new"))

	require.NoError(t, SetActiveWorkSession(""))

	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got, "unsetting the newer session must not bring the older one back")
}

func TestClearActiveWorkSessionFor_ClearsTheSessionThatEnded(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-1"))

	cleared, err := ClearActiveWorkSessionFor("ws-a", "uuid-1")

	require.NoError(t, err)
	assert.True(t, cleared)
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestClearActiveWorkSessionFor_KeepsAnotherActiveSession(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-2"))

	cleared, err := ClearActiveWorkSessionFor("ws-a", "uuid-1")

	require.NoError(t, err)
	assert.False(t, cleared)
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-2", got)
}

func TestClearActiveWorkSessionFor_KeepsTheSameIDUnderAnotherWorkspace(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-1"))

	cleared, err := ClearActiveWorkSessionFor("ws-b", "uuid-1")

	require.NoError(t, err)
	assert.False(t, cleared)
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "uuid-1", got)
}

func TestClearActiveWorkSessionFor_ReachesTheLegacyHostLabelKey(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://new-slug.us1.alpacon.io", "new-slug", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("uuid-old"))
	require.NoError(t, SetWorkspaceEnv("https://new-slug.us1.alpacon.io", "frozen", false))

	cleared, err := ClearActiveWorkSessionFor("frozen", "uuid-old")

	require.NoError(t, err)
	assert.True(t, cleared)
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestClearActiveWorkSessionFor_NoConfigIsNotAnError(t *testing.T) {
	setupTestConfig(t)

	cleared, err := ClearActiveWorkSessionFor("ws-a", "uuid-1")

	require.NoError(t, err)
	assert.False(t, cleared)
}

func TestClearActiveWorkSessionFor_NoIDTouchesNothing(t *testing.T) {
	setupTestConfig(t)

	cleared, err := ClearActiveWorkSessionFor("ws-a", "")

	require.NoError(t, err)
	assert.False(t, cleared)
	assert.NoDirExists(t, filepath.Join(os.Getenv("HOME"), ConfigFileDir))
}

func TestClearActiveWorkSessionFor_MatchesAnIDThatDiffersOnlyInCase(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))
	require.NoError(t, SetActiveWorkSession("05443c7f-0000-4000-8000-00000000abcd"))

	cleared, err := ClearActiveWorkSessionFor("ws-a", "05443C7F-0000-4000-8000-00000000ABCD")

	require.NoError(t, err)
	assert.True(t, cleared)
	got, err := GetActiveWorkSession()
	require.NoError(t, err)
	assert.Empty(t, got)
}
