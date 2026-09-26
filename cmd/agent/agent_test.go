package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentSubcommands(t *testing.T) {
	t.Parallel()
	var names []string
	for _, c := range AgentCmd.Commands() {
		names = append(names, c.Name())
	}
	assert.ElementsMatch(t, []string{"restart", "upgrade"}, names)
}

func TestAgentDisruptiveFlags(t *testing.T) {
	t.Parallel()
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
