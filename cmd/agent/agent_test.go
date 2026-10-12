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
	assert.ElementsMatch(t, []string{"restart", "upgrade", "install-pam"}, names)
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

// install-pam is not disruptive: the server applies no busy guard to it, so
// the command offers no --force to override one.
func TestAgentInstallPAMFlags(t *testing.T) {
	cmd, _, err := AgentCmd.Find([]string{"install-pam"})
	require.NoError(t, err)
	yes := cmd.Flags().Lookup("yes")
	require.NotNil(t, yes)
	assert.Equal(t, "y", yes.Shorthand)
	assert.Nil(t, cmd.Flags().Lookup("force"))
}
