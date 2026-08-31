package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xtaci/smux"
)

const (
	ConfigFileName = "config.json"
	ConfigFileDir  = ".alpacon"

	// ServiceTokenPrefix is the default service-token key prefix, hard-coded here.
	// It matches the server's default SERVICE_TOKEN_PREFIX; if a self-hosted server
	// overrides that setting, its service tokens will not match this prefix and are
	// treated as generic tokens. Personal API tokens use "alpat-".
	ServiceTokenPrefix = "alpst-"
)

func CreateConfig(workspaceURL, workspaceName, token, expiresAt, accessToken, refreshToken, baseDomain string, expiresIn int, insecure bool) error {
	config := Config{
		WorkspaceURL:  workspaceURL,
		WorkspaceName: workspaceName,
		Token:         token,
		ExpiresAt:     expiresAt,
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		BaseDomain:    baseDomain,
		Insecure:      insecure,
	}

	if expiresIn > 0 {
		config.AccessTokenExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
	}

	return saveConfig(&config)
}

// SwitchWorkspace updates the workspace URL and name in the existing config.
func SwitchWorkspace(newURL, newName string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	cfg.WorkspaceURL = newURL
	cfg.WorkspaceName = newName

	return saveConfig(&cfg)
}

// saveConfig replaces the config file in one step. Another alpacon process can
// be saving a renewed access token at the same moment—every command that meets
// an expired token writes this file—and two writers truncating the same path
// leave a config that no longer parses, which the next command reports as a
// broken file and answers with "run alpacon login". Encoding into a sibling
// temp file and renaming it over the target means a concurrent reader sees one
// whole config or the other, never a half of each. That is atomicity against
// another process and not durability against a crash: nothing flushes the temp
// file before the rename, so a power loss can still bring the config back empty
// or reverted. Two writers can still lose each other's token, which costs one
// refresh and nothing else.
func saveConfig(config *Config) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %v", err)
	}

	configDir := filepath.Join(homeDir, ConfigFileDir)
	if err = os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	file, err := os.CreateTemp(configDir, ConfigFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create config file: %v", err)
	}
	tempFile := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(tempFile)
	}()

	// The temp file holds the same credentials the config file does, so it
	// carries the same mode from the moment it exists.
	if err = file.Chmod(0600); err != nil {
		return fmt.Errorf("failed to set config file permissions: %v", err)
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "    ")
	if err = encoder.Encode(config); err != nil {
		return fmt.Errorf("failed to encode config to JSON: %v", err)
	}

	if err = file.Close(); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	configFile := filepath.Join(configDir, ConfigFileName)
	if err = os.Rename(tempFile, configFile); err != nil {
		return fmt.Errorf("failed to replace config file: %v", err)
	}

	return nil
}

func SaveRefreshedAuth0Token(accessToken string, expiresIn int) error {
	currentConfig, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load existing config: %v", err)
	}

	currentConfig.AccessToken = accessToken
	currentConfig.AccessTokenExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)

	return saveConfig(&currentConfig)
}

func DeleteConfig() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %v", err)
	}

	configDir := filepath.Join(homeDir, ConfigFileDir)
	configFile := filepath.Join(configDir, ConfigFileName)

	err = os.Remove(configFile)
	if err != nil {
		return fmt.Errorf("failed to delete config file: %v", err)
	}

	return nil
}

func LoadConfig() (Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("failed to get user home directory: %v", err)
	}

	configDir := filepath.Join(homeDir, ConfigFileDir)
	configFile := filepath.Join(configDir, ConfigFileName)

	file, err := os.Open(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			// Wrap with %w so callers can detect the missing-config case
			// via errors.Is(err, os.ErrNotExist).
			return Config{}, fmt.Errorf("config file does not exist: %s: %w", configFile, err)
		}
		return Config{}, fmt.Errorf("failed to open config file: %v", err)
	}
	defer func() { _ = file.Close() }()

	var config Config
	decoder := json.NewDecoder(file)
	if err = decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("failed to decode config file: %v", err)
	}

	return config, nil
}

// IsSaaS returns true if the workspace is an Alpacon Cloud (SaaS) deployment authenticated
// via Auth0. OnPrem deployments use a legacy API token and return false.
func IsSaaS() (bool, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return false, err
	}
	return cfg.AccessToken != "", nil
}

func (c Config) IsSaaS() bool {
	return c.AccessToken != ""
}

// SetActiveWorkSession persists the work-session UUID for the config's current workspace ("" clears it);
// a caller holding a client must use SetActiveWorkSessionFor with the client's pinned workspace instead.
func SetActiveWorkSession(uuid string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return setActiveWorkSessionOn(&cfg, cfg.WorkspaceName, uuid)
}

// SetActiveWorkSessionFor persists the work-session UUID under workspaceName ("" clears it); workspaceName
// is a parameter, not a fresh read, because create --wait --use can block on approval long
// enough for another shell's 'alpacon ws use' to file it under a workspace it was never created in.
func SetActiveWorkSessionFor(workspaceName, uuid string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return setActiveWorkSessionOn(&cfg, workspaceName, uuid)
}

// GetActiveWorkSession returns the active work-session UUID for the current workspace.
// Returns "" (no error) when no session is set, the config is missing the map, or no config file exists.
func GetActiveWorkSession() (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if cfg.ActiveWorkSessions == nil {
		return "", nil
	}
	return cfg.ActiveWorkSessions[cfg.WorkspaceName], nil
}

// IsServiceToken reports whether token is a service token, identified by its key
// prefix. Service tokens are application principals and have no user profile.
func IsServiceToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), ServiceTokenPrefix)
}

// GetAuthMethod returns a human-readable authentication method string for cfg.
func GetAuthMethod(cfg Config) string {
	if cfg.AccessToken != "" {
		return "Browser login"
	}
	if IsServiceToken(cfg.Token) {
		return "Service token"
	}
	if cfg.Token != "" {
		return "Token"
	}
	return "unknown"
}

// ResolveAuthMethod loads config and returns the auth method string.
func ResolveAuthMethod() string {
	cfg, err := LoadConfig()
	if err != nil {
		return "unknown"
	}
	return GetAuthMethod(cfg)
}

// GetSmuxConfig returns a ready-to-use smux configuration.
func GetSmuxConfig() *smux.Config {
	config := smux.DefaultConfig()
	config.KeepAliveInterval = 10 * time.Second // connection health check
	config.KeepAliveTimeout = 30 * time.Second  // abnormal connection detection
	config.MaxFrameSize = 32768                 // 32KB
	config.MaxReceiveBuffer = 4194304           // 4MB
	config.MaxStreamBuffer = 65536              // 64KB per stream
	return config
}

// setActiveWorkSessionOn shares one LoadConfig across both setters—two reads would reopen
// the closed race: workspaceName from the first read applied to a map from a second, mid-switch.
func setActiveWorkSessionOn(cfg *Config, workspaceName, uuid string) error {
	if workspaceName == "" {
		return errors.New("no active workspace; run 'alpacon login' first")
	}
	current := ""
	if cfg.ActiveWorkSessions != nil {
		current = cfg.ActiveWorkSessions[workspaceName]
	}
	if current == uuid {
		return nil
	}
	if cfg.ActiveWorkSessions == nil {
		cfg.ActiveWorkSessions = map[string]string{}
	}
	if uuid == "" {
		delete(cfg.ActiveWorkSessions, workspaceName)
	} else {
		cfg.ActiveWorkSessions[workspaceName] = uuid
	}
	return saveConfig(cfg)
}
