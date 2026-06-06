package exec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSudoDenialHint(t *testing.T) {
	t.Run("returns guidance when denial code present", func(t *testing.T) {
		out := "Alpacon denied this sudo command (SUDO_NO_WORKSESSION_POLICY).\n"
		hint := sudoDenialHint(out)
		assert.NotEmpty(t, hint)
		assert.True(t, strings.Contains(hint, "work-session update"),
			"hint should point to the work-session update command")
	})

	t.Run("presence-required points to a step-up", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_PRESENCE_REQUIRED).\n")
		assert.NotEmpty(t, hint)
		assert.True(t, strings.Contains(hint, "step-up"),
			"hint should tell the user to step up MFA")
	})

	t.Run("approval-required points to re-running after approval", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n")
		assert.NotEmpty(t, hint)
		assert.True(t, strings.Contains(hint, "approv"),
			"hint should mention the approval request")
	})

	t.Run("risk-denied is a terminal denial", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_RISK_DENIED).\n")
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

	t.Run("bare code in command output is not a false positive", func(t *testing.T) {
		// A command that merely prints the code (no denial line) must not
		// trigger a hint.
		assert.Empty(t, sudoDenialHint("echo SUDO_RISK_DENIED\nSUDO_RISK_DENIED\n"))
	})

	t.Run("forged parenthesized token is not a false positive", func(t *testing.T) {
		// A command whose own output prints the parenthesized token, without the
		// plugin's denial line, must not forge a hint (the command succeeded).
		assert.Empty(t, sudoDenialHint("echo \"(SUDO_RISK_DENIED)\"\n(SUDO_RISK_DENIED)\n"))
	})
}

func TestHasSudoPresenceDenial(t *testing.T) {
	t.Run("true on the real presence denial line", func(t *testing.T) {
		assert.True(t, hasSudoPresenceDenial(
			"Alpacon denied this sudo command (SUDO_PRESENCE_REQUIRED).\n"))
	})

	t.Run("false for other denial codes", func(t *testing.T) {
		assert.False(t, hasSudoPresenceDenial(
			"Alpacon denied this sudo command (SUDO_RISK_DENIED).\n"))
		assert.False(t, hasSudoPresenceDenial(
			"Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n"))
	})

	t.Run("false on clean output", func(t *testing.T) {
		assert.False(t, hasSudoPresenceDenial("ok\n"))
		assert.False(t, hasSudoPresenceDenial(""))
	})

	t.Run("forged parenthesized token does not trigger a step-up", func(t *testing.T) {
		// A command whose own output prints the bare token, without the plugin's
		// denial line, must not be mistaken for a presence denial.
		assert.False(t, hasSudoPresenceDenial(
			"echo \"(SUDO_PRESENCE_REQUIRED)\"\n(SUDO_PRESENCE_REQUIRED)\n"))
	})

	t.Run("true when the denial line is buried in real command output", func(t *testing.T) {
		// The denial line may be preceded by legitimate stdout; the detector
		// must still fire.
		assert.True(t, hasSudoPresenceDenial(
			"reading config...\nApplying changes\n"+
				"Alpacon denied this sudo command (SUDO_PRESENCE_REQUIRED).\n"))
	})
}
