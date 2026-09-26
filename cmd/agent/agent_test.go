package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The command-tree tests share the package-global AgentCmd, which Cobra
// mutates lazily (sorting children, building flag sets), so they stay serial.
func TestAgentSubcommands(t *testing.T) {
	var names []string
	for _, c := range AgentCmd.Commands() {
		if c.Hidden {
			continue
		}
		names = append(names, c.Name())
	}
	assert.ElementsMatch(t, []string{"restart", "upgrade"}, names)
}

func TestAgentShutdownIsAHiddenStub(t *testing.T) {
	cmd, _, err := AgentCmd.Find([]string{"shutdown"})
	require.NoError(t, err)
	assert.Equal(t, "shutdown", cmd.Name())
	assert.True(t, cmd.Hidden, "agent shutdown must stay out of --help")
	assert.Contains(t, cmd.Long, "was removed")
}

func TestAgentDisruptiveFlags(t *testing.T) {
	for _, name := range []string{"restart", "upgrade"} {
		t.Run(name, func(t *testing.T) {
			cmd, _, err := AgentCmd.Find([]string{name})
			require.NoError(t, err)
			yes := cmd.Flags().Lookup("yes")
			require.NotNil(t, yes)
			assert.Equal(t, "y", yes.Shorthand)
			assert.NotNil(t, cmd.Flags().Lookup("force"))
		})
	}
}
