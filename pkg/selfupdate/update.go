package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrWorkDirUnavailable = errors.New("cannot create the download directory") // Separates the download's scratch directory from the install path: both refuse with EACCES, and sudo answers only one of them.

type Options struct {
	ReleaseAPIURL  string
	CurrentVersion string
	GOOS           string
	GOARCH         string
	ExecutablePath string
	Runner         CommandRunner
}

type Result struct {
	Kind           InstallKind
	Guidance       string
	UpdatedTo      string
	AlreadyCurrent bool
}

func Run(opts Options) (Result, error) {
	release, err := LatestRelease(opts.ReleaseAPIURL)
	if err != nil {
		return Result{}, err
	}
	if !IsOutdated(opts.CurrentVersion, release.Version) {
		return Result{AlreadyCurrent: true}, nil
	}

	kind := DetectInstallKind(opts.Runner, opts.ExecutablePath)
	if kind != InstallManual {
		return Result{Kind: kind, Guidance: UpgradeGuidance(kind)}, nil
	}

	// The lock comes after every question that needs no write. It lives in the
	// install directory, which for a deb or an rpm belongs to root, so taking it
	// first turned "run apt-get instead" into "not writable, re-run with sudo".
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
	return Result{Kind: kind, UpdatedTo: release.Version}, nil
}
