package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xtaci/smux"
)

const (
	ConfigFileName = "config.json"
	ConfigFileDir  = ".alpacon"
)

func CreateConfig(workspaceURL, workspaceName, token, expiresAt, accessToken, refreshToken string, expiresIn int, insecure bool) error {
	config := Config{
		WorkspaceURL:  workspaceURL,
		WorkspaceName: workspaceName,
		Token:         token,
		ExpiresAt:     expiresAt,
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		Insecure:      insecure,
	}

	if expiresIn > 0 {
		config.AccessTokenExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
	}

	return saveConfig(&config)
}

func saveConfig(config *Config) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %v", err)
	}

	configDir := filepath.Join(homeDir, ConfigFileDir)
	if err = os.MkdirAll(configDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	configFile := filepath.Join(configDir, ConfigFileName)
	file, err := os.Create(configFile)
	if err != nil {
		return fmt.Errorf("failed to create config file: %v", err)
	}
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "    ")
	if err = encoder.Encode(config); err != nil {
		return fmt.Errorf("failed to encode config to JSON: %v", err)
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
			return Config{}, fmt.Errorf("config file does not exist: %v", configFile)
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
