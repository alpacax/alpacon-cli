package token

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestServiceTokenAccountHelp(t *testing.T) {
	for _, cmd := range []*cobra.Command{aclCommandAddCmd, aclFileAddCmd, aclAddCmd} {
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			username := cmd.Flags().Lookup("username").Usage
			assert.Contains(t, username, "omit to pin the entry to the application's own account")
			assert.Contains(t, username, "'' or * matches any account bound to the application on that server")
			assert.Contains(t, cmd.Flags().Lookup("groupname").Usage, "'' or * sets no group restriction")
			assert.NotContains(t, username, "same account")
			if !cmd.Hidden {
				assert.Contains(t, strings.Join(strings.Fields(cmd.Long), " "), "any account bound to the application on that server")
				assert.NotContains(t, cmd.Long, "same account")
			}
		})
	}
}
