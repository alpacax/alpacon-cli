package cmd

import (
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionFlag(t *testing.T) {
	stdout, stderr, code := runHelperProcess(t, "TestVersionFlagHelperProcess", "version-flag-helper",
		[]string{"--version"}, "GO_WANT_VERSION_FLAG_HELPER=1", homeEnvVar()+"="+t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpacon version "+utils.Version+"\n", stdout)
	assert.Empty(t, stderr)
}

func TestVersionFlagHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_VERSION_FLAG_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, "version-flag-helper")
	require.True(t, ok)
	RootCmd.SetArgs(args)
	Execute()
	os.Exit(0)
}

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
