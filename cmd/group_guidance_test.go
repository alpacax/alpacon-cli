package cmd

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGroupNoSubcommandErrorPointsToHelpInsteadOfListingSubcommands relies on
// cmd.Help() printing the live subcommand list, so the error only points to it.
func TestGroupNoSubcommandErrorPointsToHelpInsteadOfListingSubcommands(t *testing.T) {
	checked := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		// workspace groups do real work in their RunE rather than printing help.
		if c.RunE == nil || !c.HasSubCommands() || c == RootCmd || c.Parent().CommandPath() == "alpacon workspace" {
			return
		}
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		err := c.RunE(c, nil)
		c.SetOut(nil)
		c.SetErr(nil)
		checked++
		require.Error(t, err, c.CommandPath())
		assert.Contains(t, err.Error(), fmt.Sprintf("Run '%s --help'", c.CommandPath()))
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() {
				continue
			}
			assert.NotContains(t, err.Error(), sub.CommandPath(), "%s", c.CommandPath())
			assert.Contains(t, out.String(), sub.Name(), "help for %s must list %s", c.CommandPath(), sub.Name())
		}
	}
	walk(RootCmd)
	require.Greater(t, checked, 15, "expected to inspect the group commands")
}
