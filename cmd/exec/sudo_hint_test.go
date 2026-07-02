package exec

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
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

	t.Run("denial line without the trailing period does not match", func(t *testing.T) {
		// The matcher anchors on the plugin's exact line, which ends in a period.
		// A line that stops at ")" is not the plugin's output and must not match.
		assert.False(t, hasSudoPresenceDenial(
			"Alpacon denied this sudo command (SUDO_PRESENCE_REQUIRED)\n"))
	})
}

func TestHasSudoApprovalDenial(t *testing.T) {
	t.Run("true on the real approval denial line", func(t *testing.T) {
		assert.True(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n"))
	})

	t.Run("false for other denial codes", func(t *testing.T) {
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_PRESENCE_REQUIRED).\n"))
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_RISK_DENIED).\n"))
	})

	t.Run("forged parenthesized token does not trigger a pending signal", func(t *testing.T) {
		// A command whose own output prints the token, without the plugin's denial
		// line, must not be mistaken for a pending approval.
		assert.False(t, hasSudoApprovalDenial(
			"echo \"(SUDO_APPROVAL_REQUIRED)\"\n(SUDO_APPROVAL_REQUIRED)\n"))
	})

	t.Run("false on clean output", func(t *testing.T) {
		assert.False(t, hasSudoApprovalDenial("ok\n"))
		assert.False(t, hasSudoApprovalDenial(""))
	})
}

func TestIsApprovalDenial(t *testing.T) {
	const denialLine = "Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n"

	t.Run("true when the denial line accompanies a non-zero exit", func(t *testing.T) {
		assert.True(t, isApprovalDenial(&event.RemoteCommandError{ExitCode: 1, Output: denialLine}))
	})

	t.Run("true through a wrapped RemoteCommandError", func(t *testing.T) {
		wrapped := fmt.Errorf("failed to execute command: %w", &event.RemoteCommandError{ExitCode: 1, Output: denialLine})
		assert.True(t, isApprovalDenial(wrapped))
	})

	t.Run("false when the command printed the line but succeeded", func(t *testing.T) {
		// err == nil means the command did not actually get denied; a command that
		// merely echoes the denial line must not be treated as pending.
		assert.False(t, isApprovalDenial(nil))
	})

	t.Run("false for a non-approval denial", func(t *testing.T) {
		out := "Alpacon denied this sudo command (SUDO_RISK_DENIED).\n"
		assert.False(t, isApprovalDenial(&event.RemoteCommandError{ExitCode: 1, Output: out}))
	})

	t.Run("false for a plain error without the denial line", func(t *testing.T) {
		assert.False(t, isApprovalDenial(errors.New("nope")))
	})
}

func TestReRunHint(t *testing.T) {
	t.Run("minimal: server and command only", func(t *testing.T) {
		got := reRunHint(RemoteExecArgs{Server: "web-01", Command: "sudo reboot"})
		assert.Equal(t, "alpacon exec web-01 -- sudo reboot", got)
	})

	t.Run("includes user, group, and work-session", func(t *testing.T) {
		got := reRunHint(RemoteExecArgs{
			Username:      "root",
			Groupname:     "docker",
			WorkSessionID: "ses-1",
			Server:        "web-01",
			Command:       "sudo reboot",
		})
		assert.Equal(t, "alpacon exec -u root -g docker --work-session ses-1 web-01 -- sudo reboot", got)
	})
}
