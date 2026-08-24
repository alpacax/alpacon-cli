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

	if err = createDeviceIDFile(path, deviceID); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
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
// path is taken, so two concurrent invocations cannot both believe they created
// the file and the loser cannot end up on an identifier its own installation
// never sends again.
//
// The content lands before the name does: O_EXCL plus a write on the next line
// leaves the path existing and empty for the length of the write, which
// readDeviceID reports as absent, so a concurrent invocation judges it malformed
// and replaces the winner's identifier. Linking rather than renaming keeps the
// exclusion—rename replaces whatever it lands on—and publishes the temporary
// file's mode, 0600 from os.CreateTemp less the umask.
func createDeviceIDFile(path, deviceID string) error {
	tempPath, err := writeDeviceIDTempFile(path, deviceID)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()

	err = os.Link(tempPath, path)
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		// Wrapped, not reformatted: the caller matches fs.ErrExist on it.
		return fmt.Errorf("failed to create device id file: %w", err)
	}

	return createDeviceIDFileWithoutLink(path, deviceID)
}

// createDeviceIDFileWithoutLink is the fallback for a filesystem that cannot
// hard-link—FAT, exFAT, some container mounts. It gives the empty window back (a
// FAT home had 8 concurrent callers disagree in 43 of 50 rounds) and is taken
// anyway: GetOrCreateDeviceID's only caller drops the error, so failing here is
// silent and permanent. Any link failure lands here, not only an unsupported
// one, so an unrelated cause—no permission, no space—surfaces as this error.
func createDeviceIDFileWithoutLink(path, deviceID string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		// Wrapped like the link error: a concurrent invocation can take the path
		// between the failed link and this create.
		return fmt.Errorf("failed to create device id file: %w", err)
	}

	_, err = file.WriteString(deviceID + "\n")
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("failed to write device id file: %v", err)
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
// its name for a caller that publishes it with a link or a rename. A path it
// returns is the caller's to remove; on failure it removes its own.
func writeDeviceIDTempFile(path, deviceID string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), DeviceIDFileName+"-*")
	if err != nil {
		// Flattened, not wrapped: os.CreateTemp reports fs.ErrExist once it runs out
		// of names, which createDeviceID would read as the identifier file existing.
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

// restrictDeviceIDFileMode drops from a file that already exists every
// permission bit outside owner read and write. A mode argument only applies to
// a file the call creates, so an identifier file some other path left
// world-readable would otherwise keep that mode for the life of the
// installation while this package claims 0600.
//
// The mask covers the owner execute bit as well as group and other, so what
// survives is at most 0600 and the claim above is the whole truth. Nothing runs
// this file as a program, so that bit has nothing to say here.
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
	if perm&0177 == 0 {
		return nil
	}
	if err = os.Chmod(path, perm&^0177); err != nil {
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
