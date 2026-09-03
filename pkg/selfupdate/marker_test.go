package selfupdate

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial, like the rest of the Run tests: updateFixture reaches releaseServer,
// which reassigns the package-level allowedAssetOrigins, and t.Parallel() here
// races the parallel tests that read it.
func TestRunPutsTheInstallersVersionMarkerBackInStep(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)
	marker := filepath.Join(filepath.Dir(executable), installedVersionMarker)
	require.NoError(t, os.WriteFile(marker, []byte("1.3.0"), 0600))

	_, err := Run(opts)

	require.NoError(t, err)
	content, readErr := os.ReadFile(marker)
	require.NoError(t, readErr)
	assert.Equal(t, "1.4.0", string(content)) // install.ps1 writes the marker with -NoNewline.
}

func TestRunWritesNoVersionMarkerWhereTheInstallerLeftNone(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)

	_, err := Run(opts)

	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(filepath.Dir(executable), installedVersionMarker))
}

func TestRunPutsAStrandedMarkerBackInStepWithNothingToInstall(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)
	opts.CurrentVersion = "1.4.0"
	marker := filepath.Join(filepath.Dir(executable), installedVersionMarker)
	require.NoError(t, os.WriteFile(marker, []byte("1.3.0"), 0600))

	result, err := Run(opts)

	require.NoError(t, err)
	assert.True(t, result.AlreadyCurrent, "the fixture's release is the running version")
	content, readErr := os.ReadFile(marker)
	require.NoError(t, readErr)
	assert.Equal(t, "1.4.0", string(content))
}

func TestRunRecordsTheRunningBuildWhenItIsAheadOfTheRelease(t *testing.T) {
	opts, executable := updateFixture(t, noOwnerRunner)
	opts.CurrentVersion = "1.5.0"
	marker := filepath.Join(filepath.Dir(executable), installedVersionMarker)
	require.NoError(t, os.WriteFile(marker, []byte("1.3.0"), 0600))

	_, err := Run(opts)

	require.NoError(t, err)
	content, readErr := os.ReadFile(marker)
	require.NoError(t, readErr)
	assert.Equal(t, "1.5.0", string(content))
}

// The name is spelled out on both sides of a language boundary, so pin them.
func TestTheMarkerNameMatchesTheWindowsInstaller(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(filepath.Join("..", "..", "install.ps1"))
	require.NoError(t, err)

	// The assignment alone, not the whole script: Contains over 500 lines of
	// PowerShell prints every one of them when it fails. [^\r\n]* rather than
	// .*$, since git checks the file out with CRLF on Windows and $ leaves the
	// carriage return inside the match.
	assignment := regexp.MustCompile(`(?m)^\$script:VersionMarkerName = [^\r\n]*`).FindString(string(script))
	assert.Equal(t, "$script:VersionMarkerName = '"+installedVersionMarker+"'", assignment)
}
