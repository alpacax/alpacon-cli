package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrWorkDirUnavailable = errors.New("cannot create the download directory") // Tells the scratch directory's EACCES from the install path's, which sudo fixes.

// ErrUnknownVersion refuses a build no release can be placed against: compareVersions reads an
// unparseable core as 0.0.0, so a dev build would otherwise reinstall the latest release forever.
var ErrUnknownVersion = errors.New("the running build's version matches no release")

type Options struct {
	ReleaseAPIURL  string
	CurrentVersion string
	GOOS           string
	GOARCH         string
	ExecutablePath string
	Runner         CommandRunner // Defaults to ExecRunner: a zero value that skipped the ownership question would read as permission to overwrite.
}

type Result struct {
	Kind           InstallKind
	Guidance       string
	UpdatedTo      string
	AlreadyCurrent bool
}

// Windows leaves the replaced executable behind on every update, and the run
// that clears it is usually one with nothing to install—so waiting for the next
// release can mean months. A refused lock is somebody else's install.
func sweepIfPossible(executablePath string) {
	installDir, binaryName := filepath.Dir(executablePath), filepath.Base(executablePath)
	if !hasSweepableEntry(installDir, binaryName) { // Taking the lock leaves a lock file; skip it when there is nothing to sweep.
		return
	}

	lock, err := AcquireLock(LockPath(executablePath))
	if err != nil {
		return
	}
	defer func() { _ = lock.Release() }()

	SweepPreserved(installDir, binaryName)
}

func Run(opts Options) (Result, error) {
	if IsUnknownVersion(opts.CurrentVersion) {
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownVersion, opts.CurrentVersion)
	}

	release, err := LatestRelease(opts.ReleaseAPIURL)
	if err != nil {
		return Result{}, err
	}
	if !IsOutdated(opts.CurrentVersion, release.Version) {
		sweepIfPossible(opts.ExecutablePath)
		// Also the repair path for a marker an earlier update left stale. It names
		// the running build, never the release: a release candidate lands here too.
		syncVersionMarker(filepath.Dir(opts.ExecutablePath), opts.CurrentVersion)
		return Result{AlreadyCurrent: true}, nil
	}

	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner
	}

	kind, err := DetectInstallKind(runner, opts.ExecutablePath)
	if err != nil { // Not knowing is not permission: overwriting an rpm-owned binary leaves the database pointing at the version it replaced.
		return Result{}, err
	}
	if kind != InstallManual {
		return Result{Kind: kind, Guidance: UpgradeGuidance(kind)}, nil
	}

	// The lock comes after every question that needs no write: it lives in the install directory,
	// which root owns for a deb or rpm, so taking it first would answer "not writable" instead of
	// "run apt-get".
	lock, err := AcquireLock(LockPath(opts.ExecutablePath))
	if err != nil {
		return Result{Kind: kind}, err
	}
	defer func() { _ = lock.Release() }()

	installDir := filepath.Dir(opts.ExecutablePath)
	binaryName := filepath.Base(opts.ExecutablePath)
	SweepPreserved(installDir, binaryName)

	workDir, err := os.MkdirTemp("", "alpacon-update-")
	if err != nil {
		return Result{Kind: kind}, fmt.Errorf("%w: %w", ErrWorkDirUnavailable, err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	newBinary, err := FetchVerifiedBinary(release, opts.GOOS, opts.GOARCH, BinaryName(opts.GOOS), workDir)
	if err != nil {
		return Result{Kind: kind}, err
	}
	if err := ReplaceBinary(opts.ExecutablePath, newBinary); err != nil {
		return Result{Kind: kind}, err
	}
	syncVersionMarker(installDir, release.Version)
	return Result{Kind: kind, UpdatedTo: release.Version}, nil
}
