package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	// DeviceIDFileName holds the per-installation device identifier. It sits beside config.json
	// rather than inside it because logout deletes config.json and the identifier must outlive it.
	DeviceIDFileName = "device_id"
)

var (
	// deviceIDPattern mirrors the validation the Auth0 post-login action applies to the
	// `device:<id>` scope. A value that fails it is ignored server-side, so the CLI never sends
	// one: a stored value that does not match is treated as absent and replaced.
	deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
)

// IsValidDeviceID reports whether id is acceptable as a device identifier,
// applying the same rule as the Auth0 action that consumes it.
func IsValidDeviceID(id string) bool {
	return deviceIDPattern.MatchString(id)
}

// GetOrCreateDeviceID returns the stable per-installation device identifier, generating and
// persisting a new one when the file is missing or holds a malformed value. The server binds an
// MFA presence proof to it instead of an IP-based fingerprint that installations behind one
// egress IP would share.
//
// It lives in ~/.alpacon/device_id, shared by every workspace this installation logs in to and
// untouched by DeleteConfig, so a logout does not orphan the proofs bound to it.
//
// The identifier grants nothing on its own, but presence proofs bind to it and it may be recorded
// in the identity provider's logs, so the file is kept at 0600 and does not belong in logs or bug
// reports.
func GetOrCreateDeviceID() (string, error) {
	path, err := deviceIDPath()
	if err != nil {
		return "", err
	}
	configDir := filepath.Dir(path)

	warnUnrestricted("config directory", restrictConfigDirectoryMode(configDir))

	deviceID, err := readDeviceID(path)
	if err != nil {
		return "", err
	}
	if deviceID != "" {
		return deviceID, nil
	}

	if err = os.MkdirAll(configDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create config directory: %v", err)
	}

	// Not a repeat of the call above: between the two, another process can create
	// the directory this one found missing, or widen the one it narrowed, and
	// MkdirAll leaves an existing directory's mode alone. The device id is
	// written next, so the narrowing belongs after the directory exists.
	warnUnrestricted("config directory", restrictConfigDirectoryMode(configDir))

	return createDeviceID(path)
}

// readDeviceID returns the stored identifier, or "" when the file does not
// exist or holds a value the Auth0 action would reject. Surrounding whitespace
// is tolerated so a hand-edited file still works. A read failure is surfaced
// rather than reported as absent, which would hide a permissions problem behind
// an identifier that changes on every invocation.
func readDeviceID(path string) (string, error) {
	// Fails closed where the config only warns, and reads the handle it narrowed
	// rather than reopening the path: an MFA presence proof binds to whatever
	// identifier this returns, so a mode this process could not narrow means no
	// identifier rather than one another account may have chosen.
	file, err := openNarrowedConfigFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		// Say the downgrade out loud: anyone who can write in the config directory
		// can force it by leaving a link where the file belongs.
		warnOnce("device id file", err, "ignoring the device id: %v. Multi-factor presence checks fall back to a weaker network fingerprint while that stands.", err)
		return "", fmt.Errorf("cannot use the device id file: %w", err)
	}

	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("failed to read device id file: %v", err)
	}

	deviceID := strings.TrimSpace(string(data))
	if !IsValidDeviceID(deviceID) {
		return "", nil
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
		// The file blocking the create holds a malformed value, so replace it atomically: a
		// truncate in place would expose an empty file. Replacement cannot be exclusive, so
		// concurrent replacers can overwrite each other; that takes a hand-edited or truncated file
		// and simultaneous runs, and costs one login keyed to a superseded identifier, which the
		// next login corrects.
		if err = replaceDeviceIDFile(path, deviceID); err != nil {
			return "", err
		}
	}

	// Report what the file holds, not what was generated: either branch above can lose a race, and
	// every process must agree on the identifier the file will give its next run.
	stored, err := readDeviceID(path)
	if err != nil {
		return "", err
	}
	if stored == "" {
		return "", fmt.Errorf("device id file %s was removed or rewritten while being created", path)
	}

	return stored, nil
}

// createDeviceIDFile publishes deviceID at path, reporting fs.ErrExist when the path is taken,
// so two concurrent invocations cannot both believe they created the file.
//
// The content lands before the name: O_EXCL plus a write leaves the path empty for the length
// of the write, which a concurrent reader judges malformed and replaces. Linking rather than
// renaming keeps the exclusion, since rename replaces whatever it lands on, and publishes the
// temporary file's mode: 0600 less the umask on a free path, or narrower.
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

// createDeviceIDFileWithoutLink is the fallback for a filesystem that cannot hard-link—FAT, exFAT,
// some container mounts. It reopens the empty-file window but is taken anyway, since the caller
// drops GetOrCreateDeviceID's error and failing here would be silent and permanent. Any link
// failure lands here, so an unrelated cause—no permission, no space—surfaces as this error.
//
// On a Linux vfat mount whose fmask grants group or other access, readDeviceID refuses the file
// this writes, so there is no device id until the mount is remounted with fmask=0077.
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

// replaceDeviceIDFile overwrites the identifier file through a temporary file and a rename, so
// no reader observes a half-written or empty file. The replacement keeps the stricter of its own
// mode and the replaced file's—see narrowToPublishedFileMode.
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
// its name for a caller that publishes it with a link or a rename, as it stands:
// the mode is already narrowed against whatever sits at path—see
// narrowToPublishedFileMode. A path it returns is the caller's to remove; on
// failure it removes its own.
func writeDeviceIDTempFile(path, deviceID string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), DeviceIDFileName+"-*")
	if err != nil {
		// Flattened, not wrapped: os.CreateTemp reports fs.ErrExist once it runs out
		// of names, which createDeviceID would read as the identifier file existing.
		return "", fmt.Errorf("failed to create temporary device id file: %v", err)
	}
	tempPath := file.Name()

	_, err = file.WriteString(deviceID + "\n")
	if err == nil {
		err = narrowToPublishedFileMode(file, path)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("failed to prepare temporary device id file: %v", err)
	}

	return tempPath, nil
}

// narrowToPublishedFileMode drops from file every permission bit the file at path lacks, so
// publishing over that file cannot widen its mode. It runs before the publish, since a rename
// publishes whatever mode the file has at that instant, and chmods the handle rather than the
// path, which a planted symlink could redirect.
func narrowToPublishedFileMode(file *os.File, path string) error {
	if runtime.GOOS == "windows" {
		// Windows has no Unix mode bits; Chmod there toggles the read-only
		// attribute, so intersecting the modes Go synthesizes would set it
		// rather than narrow anything.
		return nil
	}

	// Nothing to stand in for: this is a fresh installation, or the file was
	// removed after the caller found the path taken.
	publishedInfo, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect device id file: %v", err)
	}
	tempInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to inspect temporary device id file: %v", err)
	}

	tempPerm := tempInfo.Mode().Perm()
	perm := tempPerm & publishedInfo.Mode().Perm()
	if perm == tempPerm {
		return nil
	}
	if err = file.Chmod(perm); err != nil {
		return fmt.Errorf("failed to restrict temporary device id file permissions: %v", err)
	}

	return nil
}

func deviceIDPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DeviceIDFileName), nil
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
