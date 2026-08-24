package approval

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

func TestApprovalGuidanceMatchesRegisteredSubcommands(t *testing.T) {
	var help bytes.Buffer
	ApprovalCmd.SetOut(&help)
	ApprovalCmd.SetErr(&help)
	t.Cleanup(func() {
		ApprovalCmd.SetOut(nil)
		ApprovalCmd.SetErr(nil)
	})

	err := ApprovalCmd.RunE(ApprovalCmd, nil)
	require.Error(t, err)
	guidance := err.Error()

	for _, sub := range ApprovalCmd.Commands() {
		name := sub.Name()
		mention := fmt.Sprintf("'alpacon approval %s'", name)
		if consoleOnlySubcommands[name] {
			assert.NotContains(t, guidance, mention)
			continue
		}
		assert.Contains(t, guidance, mention)
	}
}
