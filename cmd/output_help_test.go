package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputHelpTopicIsRegisteredAndNotRunnable(t *testing.T) {
	found, _, err := RootCmd.Find([]string{"output"})
	require.NoError(t, err)

	assert.Same(t, outputHelpCmd, found)
	assert.False(t, found.Runnable())
}

func TestOutputHelpTopicNamesEveryAlwaysPresentEnvelopeField(t *testing.T) {
	for _, field := range []string{"ok", "exit_code", "error_code", "message", "context", "context.operation"} {
		assert.Contains(t, outputHelpCmd.Long, field)
	}
}

func TestRootLongPointsToOutputHelpTopic(t *testing.T) {
	assert.Contains(t, RootCmd.Long, "alpacon help output")
}
