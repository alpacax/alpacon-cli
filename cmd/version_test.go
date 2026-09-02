package cmd

import (
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestUpgradeNoticeNamesBothVersionsAndTheUpdateCommand(t *testing.T) {
	notice := upgradeNotice("1.3.0", "1.4.0", "https://example.test/notes")

	assert.Contains(t, notice, "1.3.0")
	assert.Contains(t, notice, "1.4.0")
	assert.Contains(t, notice, "https://example.test/notes")
	assert.Contains(t, notice, "alpacon update")
}

func TestUpgradeNoticeDoesNotSendALocalBuildToTheUpdateCommand(t *testing.T) {
	notice := upgradeNotice(utils.DevVersion, "1.4.0", "https://example.test/notes")

	assert.NotContains(t, notice, "alpacon update", "'alpacon update' refuses a local build, so the notice must not name it")
	assert.Contains(t, notice, "Install a released build")
}

func TestUpgradeNoticeSaysWhatAManagedInstallWillGetInstead(t *testing.T) {
	notice := upgradeNotice("1.3.0", "1.4.0", "https://example.test/notes")

	assert.Contains(t, notice, "an install another tool owns")
}
