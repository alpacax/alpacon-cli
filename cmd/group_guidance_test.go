package cmd

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/approval"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/cmd/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupNoSubcommandErrorPointsToHelpInsteadOfListingSubcommands(t *testing.T) {
	errs := map[*cobra.Command]string{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		// workspace groups do real work in their RunE rather than printing help.
		if c.RunE == nil || !c.HasSubCommands() || c == RootCmd || c.Parent() == workspace.WorkspaceCmd {
			return
		}
		t.Run(c.CommandPath(), func(t *testing.T) {
			var out bytes.Buffer
			c.SetOut(&out)
			c.SetErr(&out)
			t.Cleanup(func() {
				c.SetOut(nil)
				c.SetErr(nil)
			})

			err := c.RunE(c, nil)

			require.Error(t, err)
			errs[c] = err.Error()
			assert.Contains(t, err.Error(), fmt.Sprintf("Run '%s --help'", c.CommandPath()))
			assert.Contains(t, out.String(), "Available Commands:")
			for _, sub := range c.Commands() {
				if !sub.IsAvailableCommand() {
					continue
				}
				for _, name := range append([]string{sub.Name()}, sub.Aliases...) {
					assert.NotContains(t, err.Error(), c.CommandPath()+" "+name)
				}
			}
		})
	}
	walk(RootCmd)

	require.GreaterOrEqual(t, len(errs), 25, "expected to inspect the group commands")
	assert.Contains(t, errs[approval.ApprovalCmd], "Alpacon console")
	assert.Contains(t, errs[worksession.WorkSessionCmd], "Alpacon console")
}
