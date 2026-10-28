package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
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

	return withConfigLock(func() error {
		return saveConfig(&config)
	})
}

// SwitchWorkspace moves from's login to newURL, saving the name, identity and Kubernetes surface
// answer in one write; once another login or switch has replaced from, it writes nothing.
func SwitchWorkspace(from Config, newURL, newName string, kubernetesSurface bool) error {
	// The new URL is built from the schema name, so it is also the identity;
	// keeping the old workspace's value would name the wrong one.
	return moveWorkspace(from.WorkspaceURL, from, Config{WorkspaceURL: newURL, WorkspaceName: newName, SchemaName: newName, KubernetesSurface: kubernetesSurface})
}

// RestoreWorkspace puts orig's workspace back in one save while the config still holds orig's login on
// switchedURL, the workspace a failed switch saved; otherwise it writes nothing.
func RestoreWorkspace(switchedURL string, orig Config) error {
	return moveWorkspace(switchedURL, orig, orig)
}

func moveWorkspace(fromURL string, login, to Config) error {
	return updateConfig(func(cfg *Config) (bool, error) {
		if cfg.WorkspaceURL != fromURL {
			return false, ErrWorkspaceChanged
		}
		if !cfg.sameLogin(login) {
			return false, ErrLoginChanged
		}
		cfg.WorkspaceURL = to.WorkspaceURL
		cfg.WorkspaceName = to.WorkspaceName
		cfg.SchemaName = to.SchemaName
		cfg.KubernetesSurface = to.KubernetesSurface
		return true, nil
	})
}

// sameLogin reports whether c holds other's credentials, which a login replaces and a switch or refresh keeps.
func (c Config) sameLogin(other Config) bool {
	return c.Token == other.Token && c.RefreshToken == other.RefreshToken
}

// saveConfig renames a sibling temp file over the config, so LoadConfig, which takes no lock, never reads half
// a file. Atomic, not durable: nothing flushes before the rename, so a power loss can empty or revert it.
func saveConfig(config *Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}

	if err = os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	warnUnrestricted("config directory", restrictConfigDirectoryMode(dir))

	// The rename below replaces whatever the last path component names, so a
	// config.json kept as a link into a dotfiles tree would be swapped for a
	// regular file on the first access-token refresh and the tree left stale.
	// Resolving first is what makes that setup survive a write, and the temp
	// file is created beside the target so the rename stays inside one
	// filesystem even when the tree is on another mount.
	configFile, err := resolveConfigWritePath(filepath.Join(dir, ConfigFileName))
	if err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(configFile), ConfigFileName+".*.tmp")
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

	if err = os.Rename(tempFile, configFile); err != nil {
		return fmt.Errorf("failed to replace config file: %v", err)
	}

	return nil
}

// ErrWorkspaceChanged reports that the config names a different workspace than
// the one a value was fetched for, so the value was not written.
var ErrWorkspaceChanged = errors.New("the config now names another workspace")

// ErrLoginChanged reports that the config holds another login than the one a
// change was decided for, so nothing was written or removed.
var ErrLoginChanged = errors.New("the config now holds another login")

// SetWorkspaceEnv records the schema name (empty keeps the stored one) and Kubernetes surface the workspace
// at workspaceURL reported, and writes nothing once another login or switch has moved the config elsewhere.
func SetWorkspaceEnv(workspaceURL, schemaName string, kubernetesSurface bool) error {
	return updateConfig(func(cfg *Config) (bool, error) {
		if cfg.WorkspaceURL != workspaceURL {
			return false, ErrWorkspaceChanged
		}
		changed := false
		if schemaName != "" && cfg.SchemaName != schemaName {
			cfg.SchemaName = schemaName
			changed = true
		}
		if cfg.KubernetesSurface != kubernetesSurface {
			cfg.KubernetesSurface = kubernetesSurface
			changed = true
		}
		return changed, nil
	})
}

// SaveRefreshedAuth0Token stores accessToken while the config still holds refreshToken; once a login made
// during the grant has replaced it, it returns ErrLoginChanged and writes nothing.
func SaveRefreshedAuth0Token(refreshToken, accessToken string, expiresIn int) error {
	return updateConfig(func(cfg *Config) (bool, error) {
		if cfg.RefreshToken != refreshToken {
			return false, ErrLoginChanged
		}
		cfg.AccessToken = accessToken
		cfg.AccessTokenExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
		return true, nil
	})
}

// DeleteConfig removes the config while it still holds login's credentials; a login another shell made since
// stays and DeleteConfig returns ErrLoginChanged. A config that is already gone is no error.
func DeleteConfig(login Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}

	return withConfigLock(func() error {
		cfg, err := LoadConfig()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if !cfg.sameLogin(login) {
			return ErrLoginChanged
		}

		// Remove what the link names, or the refresh token stays in the dotfiles tree;
		// the link itself stays, so the next login writes through it.
		configFile, err := resolveConfigWritePath(filepath.Join(dir, ConfigFileName))
		if err != nil {
			return err
		}
		if err = os.Remove(configFile); err != nil {
			return fmt.Errorf("failed to delete config file: %v", err)
		}
		return nil
	})
}

func configDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ConfigFileDir), nil
}

func LoadConfig() (Config, error) {
	dir, err := configDir()
	if err != nil {
		return Config{}, err
	}

	configFile := filepath.Join(dir, ConfigFileName)

	warnUnrestricted("config directory", restrictConfigDirectoryMode(dir))

	file, err := openCheckedConfigFile(configFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, utils.MarkNotLoggedIn(err)
		}
		return Config{}, err
	}
	defer func() { _ = file.Close() }()

	var config Config
	decoder := json.NewDecoder(file)
	if err = decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("failed to decode config file: %v", err)
	}

	return config, nil
}

// openCheckedConfigFile hands back a handle nothing else may stand in for. The
// handle the narrowing already opened is the one that is read: closing it and
// resolving the path again leaves a window for the file to be replaced between
// the checks and the decode, and costs a second open on each of the two to five
// times a command loads the config.
//
// A refusal is fatal where a failed narrowing is not. A mode this process could
// not change still leaves a file it wrote itself—a read-only mount is the usual
// reason—so that warns and the read goes on through the path. But a file
// another account owns is one another account can rewrite, and this one names
// the host the CLI talks to and decides whether its certificate is checked, so
// reading it would hand the next login's token to whoever chose those values.
func openCheckedConfigFile(configFile string) (*os.File, error) {
	file, narrowErr := openNarrowedConfigFile(configFile)
	if narrowErr == nil {
		return file, nil
	}
	if errors.Is(narrowErr, errUnsafeConfigFile) {
		return nil, narrowErr
	}
	if errors.Is(narrowErr, fs.ErrNotExist) {
		// Wrap with %w so callers can detect the missing-config case
		// via errors.Is(err, os.ErrNotExist).
		return nil, fmt.Errorf("config file does not exist: %s: %w", configFile, narrowErr)
	}
	// A symbolic link is the one failure whose exposure is still unknown here:
	// the chmod was refused on the link, and what it names has not been looked
	// at yet. Every other failure is a mode this process could not change on a
	// file it did see, so that one speaks straight away.
	symlinked := errors.Is(narrowErr, errSymlink)
	if !symlinked {
		warnUnrestricted("config file", narrowErr)
	}

	// Reached for a symbolic link above all, which is refused a chmod but not a
	// read—keeping one config.json in a dotfiles tree is a supported setup. So
	// this is the first look at what the path resolves to, and it gets the same
	// refusal the narrowed handle got: a pipe there would park the decode
	// forever, and a link is the one way a file another account owns can reach
	// this far.
	file, err := openConfigForRead(configFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("config file does not exist: %s: %w", configFile, err)
		}
		return nil, fmt.Errorf("failed to open config file: %v", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to inspect config file: %v", err)
	}
	if err = refuseUnsafeConfigFile(file, info); err != nil {
		_ = file.Close()
		return nil, err
	}
	// The target's mode is known now, so the warning can be true. A dotfiles
	// config.json kept at 0600 exposes nothing, and saying otherwise on every
	// command is how a user learns to read past the warning that matters. The
	// handle already open is what answers it: resolving the path again would
	// cost an open and read a file this one no longer has to be.
	if symlinked && grantsOtherAccounts(info.Mode().Perm()) {
		warnUnrestricted("config file", narrowErr)
	}
	return file, nil
}

// resolveConfigWritePath returns the path a write must replace. os.Rename and
// os.Remove act on the last component itself, so a config.json that is a link
// into a dotfiles tree would be swapped for a regular file by the first token
// refresh and emptied of nothing by logout.
//
// What the link names is checked the way a read checks it, since writing the
// refresh token into a file another account owns hands it over just as surely
// as reading one does.
func resolveConfigWritePath(configFile string) (string, error) {
	info, err := os.Lstat(configFile)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		// Missing, or an ordinary file: the path names what the write replaces.
		return configFile, nil
	}
	target, err := filepath.EvalSymlinks(configFile)
	if errors.Is(err, fs.ErrNotExist) {
		// A link left dangling by logout. Recreating what it names is what lets
		// the next login write through it instead of replacing it.
		return readlinkTarget(configFile)
	}
	if err != nil {
		return "", fmt.Errorf("cannot resolve config file %s: %w", configFile, err)
	}
	file, err := openToNarrow(target)
	if err != nil {
		return "", fmt.Errorf("cannot check config file %s: %w", target, err)
	}
	defer func() { _ = file.Close() }()
	targetInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to inspect config file: %v", err)
	}
	if err = refuseUnsafeConfigFile(file, targetInfo); err != nil {
		return "", err
	}
	return target, nil
}

// readlinkTarget names what a link points at, as an absolute path.
func readlinkTarget(configFile string) (string, error) {
	target, err := os.Readlink(configFile)
	if err != nil {
		return "", fmt.Errorf("cannot resolve config file %s: %w", configFile, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(configFile), target)
	}
	return target, nil
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
	return updateConfig(func(cfg *Config) (bool, error) {
		return setActiveWorkSessionOn(cfg, cfg.WorkspaceIdentity(), uuid)
	})
}

// SetActiveWorkSessionFor persists the work-session UUID under workspaceName ("" clears it); workspaceName
// is a parameter, not a fresh read, because create --wait --use can block on approval long
// enough for another shell's 'alpacon ws use' to file it under a workspace it was never created in.
func SetActiveWorkSessionFor(workspaceName, uuid string) error {
	return updateConfig(func(cfg *Config) (bool, error) {
		return setActiveWorkSessionOn(cfg, workspaceName, uuid)
	})
}

// GetActiveWorkSession returns the active work-session UUID for the current workspace, or "" when none is set or no config file exists.
// A caller holding a client uses GetActiveWorkSessionFor with the client's workspace instead.
func GetActiveWorkSession() (string, error) {
	return readActiveWorkSession(func(cfg Config) string { return cfg.WorkspaceIdentity() })
}

// GetActiveWorkSessionFor is GetActiveWorkSession for workspaceName, a parameter rather than a fresh
// read so another shell's 'alpacon ws use' cannot swap in a foreign session.
func GetActiveWorkSessionFor(workspaceName string) (string, error) {
	return readActiveWorkSession(func(Config) string { return workspaceName })
}

func readActiveWorkSession(workspace func(Config) string) (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		// errors.Is and not os.IsNotExist: LoadConfig wraps the missing-file
		// error with %w, and os.IsNotExist unwraps only the error types the os
		// package defines—so this used to report an absent config as a failure.
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return activeWorkSessionOn(cfg, workspace(cfg)), nil
}

// ClearActiveWorkSessionFor clears workspaceName's active work-session only while it is still uuid,
// so ending one session never drops another the user has since switched to. Reports whether it cleared.
func ClearActiveWorkSessionFor(workspaceName, uuid string) (bool, error) {
	if uuid == "" {
		return false, nil
	}
	cleared := false
	err := updateConfig(func(cfg *Config) (bool, error) {
		if !strings.EqualFold(activeWorkSessionOn(*cfg, workspaceName), uuid) {
			return false, nil
		}
		var err error
		cleared, err = setActiveWorkSessionOn(cfg, workspaceName, "")
		return cleared, err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return cleared, nil
}

func activeWorkSessionOn(cfg Config, workspaceName string) string {
	if uuid, ok := cfg.ActiveWorkSessions[workspaceName]; ok {
		return uuid
	}
	if workspaceName != cfg.WorkspaceIdentity() {
		return ""
	}
	// A session saved before schema_name was recorded sits under the host label.
	return cfg.ActiveWorkSessions[cfg.WorkspaceName]
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

// setActiveWorkSessionOn files uuid under workspaceName in cfg ("" clears it) and reports whether cfg changed.
func setActiveWorkSessionOn(cfg *Config, workspaceName, uuid string) (bool, error) {
	if workspaceName == "" {
		return false, errors.New("no active workspace; run 'alpacon login' first")
	}
	// A session saved before schema_name was recorded sits under the host label,
	// where a read for the current workspace still falls back to it. Every write for the
	// current workspace drops it, or an unset would revive that older session.
	legacyKey := ""
	if workspaceName == cfg.WorkspaceIdentity() && cfg.WorkspaceName != workspaceName {
		legacyKey = cfg.WorkspaceName
	}
	current := ""
	hasLegacy := false
	if cfg.ActiveWorkSessions != nil {
		current = cfg.ActiveWorkSessions[workspaceName]
		_, hasLegacy = cfg.ActiveWorkSessions[legacyKey]
		hasLegacy = hasLegacy && legacyKey != ""
	}
	if current == uuid && !hasLegacy {
		return false, nil
	}
	if cfg.ActiveWorkSessions == nil {
		cfg.ActiveWorkSessions = map[string]string{}
	}
	if hasLegacy {
		delete(cfg.ActiveWorkSessions, legacyKey)
	}
	if uuid == "" {
		delete(cfg.ActiveWorkSessions, workspaceName)
	} else {
		cfg.ActiveWorkSessions[workspaceName] = uuid
	}
	return true, nil
}
