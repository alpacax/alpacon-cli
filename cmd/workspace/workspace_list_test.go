package workspace

import (
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: sets HOME.
func TestWorkspaceList_WithoutMultiWorkspaceLoginSucceedsWithANotice(t *testing.T) {
	ios, stdout, stderr := utils.TestIOStreams()
	workspaceListCmd := newWorkspaceListCmd(ios)
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig("https://prev.us1.alpacon.io", "prev", "", "", "access-token", "refresh-token", "", 3600, false))

	err := workspaceListCmd.RunE(workspaceListCmd, nil)

	require.NoError(t, err)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "Current workspace: prev (https://prev.us1.alpacon.io)")
	assert.Contains(t, stderr.String(), "Workspace listing is available for Auth0-based logins only.")
}
