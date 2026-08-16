package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// DeviceIDFileName holds the per-installation device identifier. It sits
	// beside config.json rather than inside it so it is independent of session
	// state: logout deletes config.json, and the machine has not changed, so the
	// identifier must outlive it. This mirrors the web client, which keeps its
	// own identifier in localStorage rather than alongside its session.
	DeviceIDFileName = "device_id"
)

var (
	// deviceIDPattern mirrors the validation the Auth0 `CLI Auth` post-login
	// action applies to the `device:<id>` scope. A value that fails it is
	// rejected server-side, so the CLI never sends one: a stored value that does
	// not match is treated as absent and replaced.
	deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
)

// IsValidDeviceID reports whether id is acceptable as a device identifier,
// applying the same rule as the Auth0 action that consumes it.
func IsValidDeviceID(id string) bool {
	return deviceIDPattern.MatchString(id)
}

// GetOrCreateDeviceID returns the stable per-installation device identifier,
// generating and persisting a new one when the file is missing or holds a
// malformed value. The identifier lets the server bind an MFA presence proof to
// the CLI installation that requested the challenge; without it the server has
// to fall back to an IP-based fingerprint, which two installations behind one
// egress IP would share.
//
// It lives in ~/.alpacon/device_id, one line of plain text. That file is per
// installation rather than per workspace, so every workspace this installation
// logs in to shares the identifier, and it is untouched by DeleteConfig, so a
// logout does not orphan the presence proofs bound to it.
//
// The identifier is not a credential, but the file is still created 0600 inside
// the 0700 config directory so a shared machine is no more exposed than the
// config file already leaves it.
func GetOrCreateDeviceID() (string, error) {
	path, err := deviceIDPath()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read device id file: %v", err)
	}
	if deviceID := strings.TrimSpace(string(data)); IsValidDeviceID(deviceID) {
		return deviceID, nil
	}

	deviceID, err := newDeviceID()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("failed to create config directory: %v", err)
	}
	if err = os.WriteFile(path, []byte(deviceID+"\n"), 0600); err != nil {
		return "", fmt.Errorf("failed to write device id file: %v", err)
	}

	return deviceID, nil
}

// deviceIDPath returns the absolute path of the device identifier file.
func deviceIDPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}
	return filepath.Join(homeDir, ConfigFileDir, DeviceIDFileName), nil
}

// newDeviceID returns a random RFC 4122 version 4 UUID. The canonical
// hyphenated form is 36 characters of hex and hyphens, so it always satisfies
// deviceIDPattern.
func newDeviceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate device id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
