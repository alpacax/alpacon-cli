package cmd

import (
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/selfupdate"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpgradeNoticeNamesBothVersionsAndTheUpdateCommand(t *testing.T) {
	t.Parallel()
	notice := upgradeNotice("1.3.0", "1.4.0", "https://example.test/notes")

	assert.Contains(t, notice, "1.3.0")
	assert.Contains(t, notice, "1.4.0")
	assert.Contains(t, notice, "https://example.test/notes")
	assert.Contains(t, notice, "alpacon update")
}

func TestUpgradeNoticeDoesNotSendALocalBuildToTheUpdateCommand(t *testing.T) {
	t.Parallel()
	notice := upgradeNotice(utils.DevVersion, "1.4.0", "https://example.test/notes")

	assert.NotContains(t, notice, "alpacon update", "'alpacon update' refuses a local build, so the notice must not name it")
	assert.Contains(t, notice, "Install a released build")
}

func TestUpgradeNoticeSaysWhatAManagedInstallWillGetInstead(t *testing.T) {
	t.Parallel()
	notice := upgradeNotice("1.3.0", "1.4.0", "https://example.test/notes")

	assert.Contains(t, notice, "an install another tool owns")
}

func TestNewVersionOutputReportsTheLatestReleaseAndWhetherItIsNewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		current         string
		latest          string
		updateAvailable bool
	}{
		{name: "up to date", current: "1.4.0", latest: "1.4.0", updateAvailable: false},
		{name: "newer release", current: "1.3.0", latest: "1.4.0", updateAvailable: true},
		{name: "ahead of the release", current: "1.5.0", latest: "1.4.0", updateAvailable: false},
		{name: "local build", current: utils.DevVersion, latest: "1.4.0", updateAvailable: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			release := &selfupdate.Release{Version: tc.latest, HTMLURL: "https://example.test/notes"}

			out := newVersionOutput(tc.current, release)

			assert.Equal(t, tc.current, out.Version)
			require.NotNil(t, out.Latest)
			assert.Equal(t, tc.latest, *out.Latest)
			require.NotNil(t, out.ReleaseURL)
			assert.Equal(t, "https://example.test/notes", *out.ReleaseURL)
			assert.Equal(t, tc.updateAvailable, out.UpdateAvailable)
		})
	}
}

func TestNewVersionOutputKeepsTheCurrentVersionWhenNoReleaseWasRead(t *testing.T) {
	t.Parallel()

	out := newVersionOutput("1.4.0", nil)

	rendered, err := utils.FormatJSON(out)
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":"1.4.0","latest":null,"update_available":false,"release_url":null}`, rendered)
}

func TestVersionCommandPrintsJSONOnStdoutWithOutputJSON(t *testing.T) {
	t.Parallel()

	stdout, stderr, exitCode := runHelperProcess(t, "TestUpdateCommandHelperProcess", "update-helper",
		[]string{"version", "--output", "json"},
		"GO_WANT_UPDATE_HELPER=1", homeEnvVar()+"="+t.TempDir(),
		"ALPACON_TEST_LATEST_RELEASE=1.4.0", "ALPACON_TEST_CURRENT_VERSION=1.3.0")

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.JSONEq(t, `{"version":"1.3.0","latest":"1.4.0","update_available":true,"release_url":"https://example.test/releases"}`, stdout)
	assert.Empty(t, stderr)
}

func TestVersionCommandPrintsTheCurrentVersionAndExitsZeroWhenTheReleaseLookupFails(t *testing.T) {
	t.Parallel()

	stdout, stderr, exitCode := runHelperProcess(t, "TestUpdateCommandHelperProcess", "update-helper",
		[]string{"version", "--output", "json"},
		"GO_WANT_UPDATE_HELPER=1", homeEnvVar()+"="+t.TempDir(),
		"ALPACON_TEST_RELEASE_FAILS=1", "ALPACON_TEST_CURRENT_VERSION=1.3.0")

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.JSONEq(t, `{"version":"1.3.0","latest":null,"update_available":false,"release_url":null}`, stdout)
	assert.Contains(t, stderr, "Could not check for updates")
	assert.NotContains(t, stderr, "{")
}
