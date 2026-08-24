package worksession

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// consoleOnlySubcommands stay registered so a script calling them gets an
// actionable exit, but ADR 0015 moved the action itself to the console, so the
// no-subcommand guidance must never offer them as something to run.
var consoleOnlySubcommands = map[string]bool{"approve": true, "reject": true}

func TestWorkSessionGuidanceMatchesRegisteredSubcommands(t *testing.T) {
	var help bytes.Buffer
	WorkSessionCmd.SetOut(&help)
	WorkSessionCmd.SetErr(&help)
	t.Cleanup(func() {
		WorkSessionCmd.SetOut(nil)
		WorkSessionCmd.SetErr(nil)
	})

	err := WorkSessionCmd.RunE(WorkSessionCmd, nil)
	require.Error(t, err)
	guidance := err.Error()

	for _, sub := range WorkSessionCmd.Commands() {
		name := sub.Name()
		mention := fmt.Sprintf("'alpacon work-session %s'", name)
		if consoleOnlySubcommands[name] {
			assert.NotContains(t, guidance, mention)
			continue
		}
		assert.Contains(t, guidance, mention)
	}
}
