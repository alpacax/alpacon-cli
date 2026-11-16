package cmd

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/approval"
	"github.com/alpacax/alpacon-cli/cmd/event"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/cmd/workspace"
	"github.com/alpacax/alpacon-cli/utils"
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
		// workspace and its groups and the deprecated event list do real work in their RunE rather than
		// printing help, and so do exec and websh, which parse their own arguments.
		if c.RunE == nil || !c.HasSubCommands() || c == RootCmd || c == workspace.WorkspaceCmd || c.Parent() == workspace.WorkspaceCmd || c == event.EventCmd || c.DisableFlagParsing {
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
			assert.True(t, utils.IsUsageError(err), "a missing subcommand must exit 2: %v", err)
			assert.Equal(t, utils.ErrorCodeUsage, utils.ClassifyError(err))
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

func TestEveryGroupRefusesAWordThatNamesNoSubcommand(t *testing.T) {
	original := map[*cobra.Command]cobra.PositionalArgs{}
	var save func(*cobra.Command)
	save = func(c *cobra.Command) {
		original[c] = c.Args
		for _, sub := range c.Commands() {
			save(sub)
		}
	}
	save(RootCmd)
	t.Cleanup(func() {
		for c, args := range original {
			c.Args = args
		}
	})
	ownRules := []string{"alpacon exec", "alpacon websh", "alpacon completion"}
	rejectUnknownSubcommands(RootCmd)
	count := 0
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c == RootCmd || !c.HasSubCommands() || slices.Contains(ownRules, c.CommandPath()) {
			return
		}
		count++
		t.Run(c.CommandPath(), func(t *testing.T) {
			assert.True(t, c.Runnable(), "a group with no Run skips its Args check: %s", c.CommandPath())
			err := c.ValidateArgs([]string{"zzzzzz"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown command")
		})
	}
	walk(RootCmd)
	require.GreaterOrEqual(t, count, 25)
}
