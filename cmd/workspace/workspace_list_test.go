package workspace

import (
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: sets HOME, swaps os.Stderr and runs a package-level command.
func TestWorkspaceList_WithoutMultiWorkspaceLoginSucceedsWithANotice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig("https://prev.us1.alpacon.io", "prev", "", "", "access-token", "refresh-token", "", 3600, false))

	var err error
	stdout, stderr := testutil.CaptureOutput(t, func() {
		err = workspaceListCmd.RunE(workspaceListCmd, nil)
	})

	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Current workspace: prev (https://prev.us1.alpacon.io)")
	assert.Contains(t, stderr, "Workspace listing is available for Auth0-based logins only.")
}
