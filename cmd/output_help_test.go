package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputHelpTopicIsRegisteredAndStatesTheContract(t *testing.T) {
	found, _, err := RootCmd.Find([]string{"output"})
	require.NoError(t, err)

	assert.Same(t, outputHelpCmd, found)
	assert.False(t, found.Runnable())
	assert.Contains(t, found.Long, "error_code")
	assert.Contains(t, found.Long, "last line")
	assert.Contains(t, found.Long, "stderr")
	assert.Contains(t, found.Long, "shell completion script")
	assert.Contains(t, found.Long, "event watch")
	assert.Contains(t, found.Long, "alpacon api prints")
}

func TestRootLongPointsToOutputHelpTopic(t *testing.T) {
	assert.Contains(t, RootCmd.Long, "alpacon help output")
}
