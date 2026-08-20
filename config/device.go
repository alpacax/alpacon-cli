package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
	// deviceIDPattern mirrors the validation the Auth0 `Add Completed MFA
	// Claim` post-login action applies to the `device:<id>` scope—that action
	// is the one that consumes the identifier; `CLI Auth` reads `org:<name>`
	// and never looks at this scope. A value that fails the pattern is ignored
	// server-side, so the CLI never sends one: a stored value that does not
	// match is treated as absent and replaced.
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
// The identifier is not a secret in the sense a password or a token is—knowing
// it grants nothing to someone who cannot also authenticate as the user—but it
// is not inert either. Under ADR 0054 it is the value an MFA presence proof
// binds to, and it identifies this installation to the identity provider,
// travelling there in OAuth requests where it may be recorded in tenant logs.
// So it is a stable, security-relevant identifier rather than a throwaway: the
// file is kept at 0600 inside the 0700 config directory, out of reach of other
// accounts on a shared machine, and it does not belong in logs or bug reports.
func GetOrCreateDeviceID() (string, error) {
	path, err := deviceIDPath()
	if err != nil {
		return "", err
	}

	deviceID, err := readDeviceID(path)
	if err != nil {
		return "", err
	}
	if deviceID != "" {
		return deviceID, nil
	}

	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("failed to create config directory: %v", err)
	}

	return createDeviceID(path)
}

// readDeviceID returns the stored identifier, or "" when the file does not
// exist or holds a value the Auth0 action would reject. Surrounding whitespace
// is tolerated so a hand-edited file still works. A read failure is surfaced
// rather than reported as absent, which would hide a permissions problem behind
// an identifier that changes on every invocation.
func readDeviceID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read device id file: %v", err)
	}

	deviceID := strings.TrimSpace(string(data))
	if !IsValidDeviceID(deviceID) {
		return "", nil
	}
	if err = restrictDeviceIDFileMode(path); err != nil {
		return "", err
	}

	return deviceID, nil
}

// createDeviceID persists a freshly generated identifier and returns the one
// that ended up on disk.
func createDeviceID(path string) (string, error) {
	deviceID, err := newDeviceID()
	if err != nil {
		return "", err
	}

	err = createDeviceIDFile(path, deviceID)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrExist):
		// Something created the file between the read in GetOrCreateDeviceID
		// and this call. Whatever it holds is already in flight, so adopt it
		// instead of overwriting it with a value only this process knows.
		existing, readErr := readDeviceID(path)
		if readErr != nil {
			return "", readErr
		}
		if existing != "" {
			return existing, nil
		}
		// The file blocking the create holds a value the Auth0 action would
		// reject, so it has to go—but atomically, since a truncate-in-place
		// would expose an empty file to whoever reads it next.
		//
		// Replacing cannot be made exclusive the way creating is: there is no
		// portable compare-and-swap on a path, so two invocations that both
		// find a malformed file both replace it and the second overwrites the
		// first. Only the malformed case is exposed to that—reaching it takes a
		// hand-edited or truncated file and concurrent invocations at the same
		// moment—and its cost is one login keyed to a superseded identifier,
		// which the next login corrects. Creation, the case that happens on
		// every fresh installation, is exclusive and has no such window.
		if err = replaceDeviceIDFile(path, deviceID); err != nil {
			return "", err
		}
	default:
		return "", err
	}

	// Report what the file holds rather than what was generated. Both of the
	// branches above can lose a race to a concurrent invocation, and the file
	// is the only thing either process will read on its next run, so the two
	// must agree on it now rather than authenticate under identifiers that
	// disagree.
	stored, err := readDeviceID(path)
	if err != nil {
		return "", err
	}
	if stored == "" {
		return "", fmt.Errorf("device id file %s was removed or rewritten while being created", path)
	}

	return stored, nil
}

// createDeviceIDFile publishes deviceID at path, reporting fs.ErrExist when the
// path is already taken, so two concurrent invocations cannot both believe they
// created the file. A plain write would let both generate and both store, and
// the loser would authenticate under an identifier its own installation never
// sends again, orphaning that login's presence.
//
// The content lands before the name does: the value is written to a temporary
// file, and the link publishes a file that already holds it. Creating with
// O_EXCL and writing on the next line is exclusive on the name alone—for the
// length of that write the path exists and is empty, and an empty file is what
// readDeviceID reports as absent, so a concurrent invocation reads it, judges
// it malformed, and replaces the winner's identifier with its own.
//
// Linking rather than renaming is what keeps the exclusion: rename replaces
// whatever it lands on. A filesystem with no hard links fails here rather than
// falling back, since every fallback either gives up the exclusion or brings
// the empty window back. That failure costs a hardening signal rather than a
// login: the one caller of GetOrCreateDeviceID drops the error and lets the
// server fall back to its own fingerprint.
//
// The mode published is the temporary file's, which os.CreateTemp sets to 0600
// and the umask can narrow further.
func createDeviceIDFile(path, deviceID string) error {
	tempPath, err := writeDeviceIDTempFile(path, deviceID)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()

	if err = os.Link(tempPath, path); err != nil {
		// Wrapped, not reformatted: the caller matches fs.ErrExist on it.
		return fmt.Errorf("failed to create device id file: %w", err)
	}

	return nil
}

// replaceDeviceIDFile overwrites the identifier file through a temporary file
// and a rename, so no reader observes a half-written or empty file and so the
// replacement carries its own mode instead of inheriting whatever the file it
// replaced had.
func replaceDeviceIDFile(path, deviceID string) error {
	tempPath, err := writeDeviceIDTempFile(path, deviceID)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()

	if err = os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("failed to replace device id file: %v", err)
	}

	return nil
}

// writeDeviceIDTempFile writes deviceID to a fresh file beside path and returns
// that file's name, for a caller that publishes it with a link or a rename. A
// returned path is the caller's to remove: after a link it is a second name for
// a file that is now published, and after a failed publication it is a leftover.
// Nothing is returned on failure here—this function removes its own.
func writeDeviceIDTempFile(path, deviceID string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), DeviceIDFileName+"-*")
	if err != nil {
		// Flattened rather than wrapped, deliberately: os.CreateTemp reports
		// fs.ErrExist once it runs out of names to try, and createDeviceID reads
		// that error as the identifier file itself already existing.
		return "", fmt.Errorf("failed to create device id file: %v", err)
	}
	tempPath := file.Name()

	_, err = file.WriteString(deviceID + "\n")
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("failed to write device id file: %v", err)
	}

	return tempPath, nil
}

// restrictDeviceIDFileMode drops any group or other permission bits from a file
// that already exists. A mode argument only applies to a file the call creates,
// so an identifier file some other path left world-readable would otherwise
// keep that mode for the life of the installation while this package claims
// 0600.
//
// Bits are only ever removed, never added: a stricter umask legitimately
// produces something tighter than 0600, and loosening that back would be this
// function undoing the protection it exists to provide.
func restrictDeviceIDFileMode(path string) error {
	if runtime.GOOS == "windows" {
		// Windows has no Unix mode bits; os.Chmod there toggles the read-only
		// attribute, which is not what this is about.
		return nil
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to inspect device id file: %v", err)
	}
	perm := fileInfo.Mode().Perm()
	if perm&0077 == 0 {
		return nil
	}
	if err = os.Chmod(path, perm&^0077); err != nil {
		return fmt.Errorf("failed to restrict device id file permissions: %v", err)
	}

	return nil
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
