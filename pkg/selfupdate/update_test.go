package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func updateFixture(t *testing.T, runner CommandRunner) (Options, string) {
	t.Helper()

	dir := t.TempDir()
	release := releaseServer(t, dir, false)

	installDir := t.TempDir()
	executable := filepath.Join(installDir, "alpacon")
	require.NoError(t, os.WriteFile(executable, []byte("old binary"), 0755))

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{"tag_name": "v" + release.Version, "html_url": "https://example.test/notes"}
		assets := make([]map[string]string, 0, len(release.Assets))
		for _, asset := range release.Assets {
			assets = append(assets, map[string]string{"name": asset.Name, "browser_download_url": asset.DownloadURL})
		}
		payload["assets"] = assets
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(api.Close)

	return Options{
		ReleaseAPIURL:  api.URL,
		CurrentVersion: "1.3.0",
		GOOS:           "linux",
		GOARCH:         "amd64",
		ExecutablePath: executable,
		Runner:         runner,
	}, executable
}

func noOwnerRunner(name string, args ...string) ([]byte, error) {
	return nil, errors.New("exit status 1")
}

func TestRunReplacesAManualInstall(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)

	result, err := Run(opts)

	require.NoError(t, err)
	assert.Equal(t, InstallManual, result.Kind)
	assert.Equal(t, "1.4.0", result.UpdatedTo)
	content, readErr := os.ReadFile(executable)
	require.NoError(t, readErr)
	assert.Equal(t, "new binary", string(content))
}

func TestRunReleasesTheLockItTook(t *testing.T) {
	opts, _ := updateFixture(t, noOwnerRunner)

	_, err := Run(opts)
	require.NoError(t, err)

	lock, err := AcquireLock(LockPath(opts.ExecutablePath))
	require.NoError(t, err, "a lock still held after Run returned would refuse every later update in this process")
	require.NoError(t, lock.Release())
}

func debRunner(called *[]string) CommandRunner {
	return func(name string, args ...string) ([]byte, error) {
		*called = append(*called, name)
		if name == "dpkg" {
			return []byte("alpacon: /usr/local/bin/alpacon\n"), nil
		}
		return nil, errors.New("exit status 1")
	}
}

func TestRunLeavesAPackageManagerInstallAlone(t *testing.T) {
	var called []string
	opts, executable := updateFixture(t, debRunner(&called))

	result, err := Run(opts)

	require.NoError(t, err)
	assert.Equal(t, InstallDeb, result.Kind)
	assert.Contains(t, result.Guidance, "sudo apt-get")
	assert.Empty(t, result.UpdatedTo)
	content, readErr := os.ReadFile(executable)
	require.NoError(t, readErr)
	assert.Equal(t, "old binary", string(content), "a package-manager install must not be replaced")
	assert.NotContains(t, called, "sudo", "the CLI must never run sudo itself")
	assert.NotContains(t, called, "apt-get", "the CLI must never run the package manager itself")
}

func TestRunStopsWhenTheRunningVersionIsNewest(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)
	opts.CurrentVersion = "1.4.0"

	result, err := Run(opts)

	require.NoError(t, err)
	assert.Equal(t, Result{AlreadyCurrent: true}, result)
	content, readErr := os.ReadFile(executable)
	require.NoError(t, readErr)
	assert.Equal(t, "old binary", string(content))
}

func TestRunRefusesWhileAnotherUpdateHoldsTheLock(t *testing.T) {
	opts, _ := updateFixture(t, noOwnerRunner)
	held, err := AcquireLock(LockPath(opts.ExecutablePath))
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })

	_, err = Run(opts)

	assert.ErrorIs(t, err, ErrUpdateInProgress)
}

func TestRunSweepsWhatAnEarlierUpdateCouldNotDelete(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)
	installDir := filepath.Dir(executable) // Windows leaves the running executable behind on every update, so this sweep is the only thing that ever removes it.
	leftovers := []string{"alpacon.old.1735689600000000000", "alpacon.staged.1735689600000000000"}
	for _, name := range leftovers {
		require.NoError(t, os.WriteFile(filepath.Join(installDir, name), []byte("x"), 0600))
	}

	_, err := Run(opts)

	require.NoError(t, err)
	for _, name := range leftovers {
		_, statErr := os.Stat(filepath.Join(installDir, name))
		assert.ErrorIs(t, statErr, os.ErrNotExist, "an update must clear %s", name)
	}
}

func TestRunNamesThePackageManagerEvenWhenItOwnsTheInstallDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod does not take the write bit off a Windows directory, so nothing here would be refused")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a directory with no write bit, so the refusal this test needs never happens")
	}

	var called []string
	opts, executable := updateFixture(t, debRunner(&called))
	installDir := filepath.Dir(executable)
	require.NoError(t, os.Chmod(installDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(installDir, 0700) })

	result, err := Run(opts)

	require.NoError(t, err, "the answer is apt-get, and reaching it must not require a writable install directory")
	assert.Equal(t, InstallDeb, result.Kind)
	assert.Contains(t, result.Guidance, "sudo apt-get")
}

func TestRunNamesTheTemporaryDirectoryWhenItCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod does not take the write bit off a Windows directory, so nothing here would be refused")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a directory with no write bit, so the refusal this test needs never happens")
	}

	opts, _ := updateFixture(t, noOwnerRunner)
	readOnly := filepath.Join(t.TempDir(), "tmp")
	require.NoError(t, os.Mkdir(readOnly, 0500))
	t.Setenv("TMPDIR", readOnly)

	_, err := Run(opts)

	require.ErrorIs(t, err, ErrWorkDirUnavailable)
	assert.ErrorIs(t, err, os.ErrPermission)
}

// The command guards this too, but the package is public and an importer may not.
func TestRunRefusesABuildThatMatchesNoRelease(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)
	opts.CurrentVersion = "dev"

	_, err := Run(opts)

	require.ErrorIs(t, err, ErrUnknownVersion)
	content, readErr := os.ReadFile(executable)
	require.NoError(t, readErr)
	assert.Equal(t, "old binary", string(content))
}

// The run that clears a Windows leftover is usually one with nothing to install.
func TestRunSweepsEvenWhenThereIsNothingToInstall(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)
	opts.CurrentVersion = "1.4.0"
	installDir := filepath.Dir(executable)
	leftover := filepath.Join(installDir, "alpacon.old.1735689600000000000")
	require.NoError(t, os.WriteFile(leftover, []byte("x"), 0600))

	result, err := Run(opts)

	require.NoError(t, err)
	assert.Equal(t, Result{AlreadyCurrent: true}, result)
	_, statErr := os.Stat(leftover)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

// Not knowing is not permission: overwriting an rpm-owned binary leaves the
// database pointing at the version it replaced.
func TestRunLeavesTheBinaryAloneWhenItCannotTellWhoOwnsIt(t *testing.T) {
	stalled := func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("%w: %s: %w", ErrOwnerUnknown, name, context.DeadlineExceeded)
	}
	opts, executable := updateFixture(t, stalled)

	_, err := Run(opts)

	require.ErrorIs(t, err, ErrOwnerUnknown)
	content, readErr := os.ReadFile(executable)
	require.NoError(t, readErr)
	assert.Equal(t, "old binary", string(content))
}

// The lock file outlives the run, and an install another tool owns never has
// anything to sweep—so a run that finds nothing must leave the directory alone.
func TestRunWritesNoLockFileWhenThereIsNothingToSweep(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)
	opts.CurrentVersion = "1.4.0"

	_, err := Run(opts)

	require.NoError(t, err)
	_, statErr := os.Stat(LockPath(executable))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

// Options.Runner has a zero value, and a caller who leaves it there must not
// get "nobody owns it" for free—the real query answers instead.
func TestRunAsksTheRealQueryWhenTheCallerSuppliedNone(t *testing.T) {
	opts, executable := updateFixture(t, nil)

	result, err := Run(opts)

	require.NoError(t, err, "a nil runner must not surface as ErrOwnerUnknown")
	assert.Equal(t, InstallManual, result.Kind, "no package owns a binary in a temp directory")
	content, readErr := os.ReadFile(executable)
	require.NoError(t, readErr)
	assert.Equal(t, "new binary", string(content))
}
