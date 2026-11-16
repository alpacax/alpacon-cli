package token

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestAPITokenAccountHelp(t *testing.T) {
	for _, cmd := range []*cobra.Command{aclCommandAddCmd, aclFileAddCmd, aclAddCmd} {
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			for _, help := range []string{cmd.Long, cmd.Flags().Lookup("username").Usage, cmd.Flags().Lookup("groupname").Usage} {
				assert.NotContains(t, help, "application")
				assert.NotContains(t, help, "Service tokens:")
				assert.NotContains(t, help, "For a service token")
			}
		})
	}
}
