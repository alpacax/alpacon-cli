package selfupdate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpgradeGuidance(t *testing.T) {
	tests := []struct {
		name         string
		kind         InstallKind
		wantContains string
	}{
		{name: "homebrew", kind: InstallHomebrew, wantContains: "brew upgrade alpacon-cli"},
		{name: "deb", kind: InstallDeb, wantContains: "sudo apt-get install --only-upgrade alpacon"},
		{name: "rpm", kind: InstallRPM, wantContains: "sudo yum update alpacon"},
		{name: "version manager", kind: InstallVersionManager, wantContains: "version manager"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, UpgradeGuidance(tt.kind), tt.wantContains)
		})
	}
}

func TestUpgradeGuidanceIsEmptyForAManualInstall(t *testing.T) {
	assert.Empty(t, UpgradeGuidance(InstallManual), "a manual install is replaced by the CLI, so there is nothing to tell the user to run")
}

func TestUpgradeGuidanceAlwaysAnswersForANonManualInstall(t *testing.T) {
	guidance := UpgradeGuidance(InstallKind("snap"))

	assert.NotEmpty(t, guidance)
	assert.Contains(t, guidance, "snap")
	assert.Empty(t, UpgradeGuidance(InstallManual), "a manual install is the one this command does replace")
}
