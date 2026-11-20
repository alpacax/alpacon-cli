package token

import (
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPITokenAccountHelp(t *testing.T) {
	t.Parallel()
	for _, path := range [][]string{{"acl", "command", "add"}, {"acl", "file", "add"}, {"acl", "add"}} {
		cmd, _, err := NewTokenCmd(cmdtest.NoClient, utils.Stdio).Find(path)
		require.NoError(t, err)
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			t.Parallel()
			for _, help := range []string{cmd.Long, cmd.Flags().Lookup("username").Usage, cmd.Flags().Lookup("groupname").Usage} {
				assert.NotContains(t, help, "application")
				assert.NotContains(t, help, "Service tokens:")
				assert.NotContains(t, help, "For a service token")
			}
		})
	}
}
