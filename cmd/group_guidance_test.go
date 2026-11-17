package cmd

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runHint matches the "Run 'alpacon ...'" sentences that tell a user which command to type next.
var runHint = regexp.MustCompile(`Run '(?:alpacon|ac) ([^']*)'`)

// findCommand returns the command at path under root, failing the test when there is none.
func findCommand(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	found, _, err := root.Find(path)
	require.NoError(t, err)
	return found
}

func TestGroupNoSubcommandErrorPointsToHelpInsteadOfListingSubcommands(t *testing.T) {
	t.Parallel()
	root := newTestRoot()
	workspaceCmd := findCommand(t, root, "workspace")
	errs := map[*cobra.Command]string{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		// workspace and its groups do real work in their RunE rather than printing help,
		// and so do exec and websh, which parse their own arguments.
		if c.RunE == nil || !c.HasSubCommands() || c == root || c == workspaceCmd || c.Parent() == workspaceCmd || c.DisableFlagParsing {
			return
		}
		t.Run(c.CommandPath(), func(t *testing.T) {
			var out bytes.Buffer
			c.SetOut(&out)
			c.SetErr(&out)

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
	walk(root)

	require.GreaterOrEqual(t, len(errs), 25, "expected to inspect the group commands")
	assert.Contains(t, errs[findCommand(t, root, "approval")], "Alpacon console")
	assert.Contains(t, errs[findCommand(t, root, "work-session")], "Alpacon console")
	assert.Contains(t, errs[findCommand(t, root, "event")], "alpacon exec ls")
}

func TestEveryGroupRefusesAWordThatNamesNoSubcommand(t *testing.T) {
	t.Parallel()
	root := newTestRoot()
	ownRules := []string{"alpacon exec", "alpacon websh", "alpacon completion"}
	rejectUnknownSubcommands(root)
	count := 0
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c == root || !c.HasSubCommands() || slices.Contains(ownRules, c.CommandPath()) {
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
	walk(root)
	require.GreaterOrEqual(t, count, 25)
}

func TestRunHintsNameACommandThatRunsAsTyped(t *testing.T) {
	t.Parallel()
	root := newTestRoot()
	workspaceCmd := findCommand(t, root, "workspace")
	var hints []string
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != ".." && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir // .git, .github and other tool directories hold no Go sources
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range runHint.FindAllStringSubmatch(string(src), -1) {
			hints = append(hints, m[1])
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, hints)

	for _, hint := range hints {
		t.Run(hint, func(t *testing.T) {
			words := strings.Fields(hint)
			found, _, err := root.Find(words)

			require.NoError(t, err)
			require.NotEqual(t, root, found, "hint names no command")
			if found.HasAvailableSubCommands() && !slices.Contains(words, "--help") && found != workspaceCmd {
				t.Errorf("'alpacon %s' reaches the group %q, which needs a subcommand", hint, found.CommandPath())
			}
			for _, word := range words {
				name, isFlag := strings.CutPrefix(word, "--")
				name, _, _ = strings.Cut(name, "=")
				if isFlag && name != "" && found.Flags().Lookup(name) == nil && found.InheritedFlags().Lookup(name) == nil {
					t.Errorf("'alpacon %s' passes --%s, which %q does not define", hint, name, found.CommandPath())
				}
			}
		})
	}
}
