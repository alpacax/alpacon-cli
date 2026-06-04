package exec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSudoDenialHint(t *testing.T) {
	t.Run("returns guidance when denial code present", func(t *testing.T) {
		out := "sudo: Permission denied (SUDO_NO_WORKSESSION_POLICY)\n"
		hint := sudoDenialHint(out)
		assert.NotEmpty(t, hint)
		assert.True(t, strings.Contains(hint, "work-session update"),
			"hint should point to the work-session update command")
	})

	t.Run("presence-required points to a step-up", func(t *testing.T) {
		hint := sudoDenialHint("sudo: Permission denied (SUDO_PRESENCE_REQUIRED)\n")
		assert.NotEmpty(t, hint)
		assert.True(t, strings.Contains(hint, "step-up"),
			"hint should tell the user to step up MFA")
	})

	t.Run("approval-required points to re-running after approval", func(t *testing.T) {
		hint := sudoDenialHint("sudo: Permission denied (SUDO_APPROVAL_REQUIRED)\n")
		assert.NotEmpty(t, hint)
		assert.True(t, strings.Contains(hint, "approv"),
			"hint should mention the approval request")
	})

	t.Run("risk-denied is a terminal denial", func(t *testing.T) {
		hint := sudoDenialHint("sudo: Permission denied (SUDO_RISK_DENIED)\n")
		assert.NotEmpty(t, hint)
		assert.True(t, strings.Contains(hint, "risk"),
			"hint should name the risk assessment")
		// Disclosure: never echo a score/reasoning, only the category.
		assert.False(t, strings.Contains(hint, "score"))
	})

	t.Run("empty when no denial code", func(t *testing.T) {
		assert.Empty(t, sudoDenialHint("ok\n"))
		assert.Empty(t, sudoDenialHint(""))
	})
}
