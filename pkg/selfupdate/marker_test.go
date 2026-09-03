package selfupdate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	script, err := os.ReadFile(filepath.Join("..", "..", "install.ps1"))
	require.NoError(t, err)

	assert.Contains(t, string(script), "$script:VersionMarkerName = '"+installedVersionMarker+"'")
}
