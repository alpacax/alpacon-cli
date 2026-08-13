package exec

import (
	"errors"
	"fmt"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/stretchr/testify/assert"
)

func TestSudoDenialHint(t *testing.T) {
	t.Run("returns guidance when denial code present", func(t *testing.T) {
		out := "Alpacon denied this sudo command (SUDO_NO_WORKSESSION_POLICY).\n"
		hint := sudoDenialHint(out)
		assert.Contains(t, hint, "work-session update")
	})

	t.Run("presence-required points to a step-up", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_PRESENCE_REQUIRED).\n")
		assert.Contains(t, hint, "step-up")
	})

	t.Run("approval-required points to re-running after approval", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n")
		assert.Contains(t, hint, "approv")
	})

	t.Run("policy-mfa-required reads differently from the no-policy hint", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_POLICY_MFA_REQUIRED).\n")
		assert.Contains(t, hint, "MFA")
		assert.Contains(t, hint, "work-session update")
		assert.NotEqual(t, sudoDenialHint("Alpacon denied this sudo command (SUDO_NO_WORKSESSION_POLICY).\n"), hint)
	})

	t.Run("intent-deviation offers only the edit that skips the wait", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_INTENT_DEVIATION).\n")
		assert.Contains(t, hint, "approval request")
		// The AI judges the command against both the session title and its
		// description (WorkSessionRiskContext), but only a title edit applies at
		// once: a description edit on an approved/active session is queued for an
		// approval of its own, which is the wait this path exists to skip.
		assert.Contains(t, hint, "work-session update [SESSION_ID] --title")
		assert.Contains(t, hint, "queues its own approval")
		assert.NotContains(t, hint, `--description "`, "a queued edit must not read as part of the reviewer-free command")
	})

	t.Run("risk-denied is a terminal denial", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_RISK_DENIED).\n")
		assert.Contains(t, hint, "risk")
		// Disclosure: never echo a score/reasoning, only the category.
		assert.NotContains(t, hint, "score")
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

	t.Run("true on an intent-deviation denial", func(t *testing.T) {
		// The server takes the same HITL branch and only swaps the code
		// (sudo/services.py), so an approval request is in flight here too.
		assert.True(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_INTENT_DEVIATION).\n"))
	})

	t.Run("false for other denial codes", func(t *testing.T) {
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_PRESENCE_REQUIRED).\n"))
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_RISK_DENIED).\n"))
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_POLICY_MFA_REQUIRED).\n"))
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

func TestPendingSudoDenialHint(t *testing.T) {
	t.Run("answers with the pending code, not an earlier terminal one", func(t *testing.T) {
		// One command line can run several sudo calls and carry a denial line for
		// each. The pending path classifies on the pendingApproval codes alone, so
		// its hint must come from the code that made it pending—here the intent
		// deviation, not the terminal no-policy denial that sits earlier in the
		// table and is what sudoDenialHint returns for the same output.
		out := "Alpacon denied this sudo command (SUDO_NO_WORKSESSION_POLICY).\n" +
			"Alpacon denied this sudo command (SUDO_INTENT_DEVIATION).\n"
		assert.Contains(t, pendingSudoDenialHint(out), "--title")
		assert.Contains(t, sudoDenialHint(out), "--sudo")
	})

	t.Run("empty when the output carries no pending code", func(t *testing.T) {
		assert.Empty(t, pendingSudoDenialHint(
			"Alpacon denied this sudo command (SUDO_RISK_DENIED).\n"))
		assert.Empty(t, pendingSudoDenialHint(""))
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

	t.Run("true for an intent-deviation denial", func(t *testing.T) {
		out := "Alpacon denied this sudo command (SUDO_INTENT_DEVIATION).\n"
		assert.True(t, isApprovalDenial(&event.RemoteCommandError{ExitCode: 1, Output: out}))
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
		assert.Equal(t, "alpacon exec web-01 -- sudo reboot", got.Command)
		assert.Empty(t, got.Description, "no --env keys means no caveat")
	})

	t.Run("includes user, group, and work-session", func(t *testing.T) {
		got := reRunHint(RemoteExecArgs{
			Username:      "root",
			Groupname:     "docker",
			WorkSessionID: "ses-1",
			Server:        "web-01",
			Command:       "sudo reboot",
		})
		assert.Equal(t, "alpacon exec -u root -g docker --work-session ses-1 web-01 -- sudo reboot", got.Command)
	})

	t.Run("emits env keys sorted, never values, with a shell-reread caveat", func(t *testing.T) {
		got := reRunHint(RemoteExecArgs{
			Server:  "web-01",
			Command: "sudo reboot",
			Env:     map[string]string{"PGPASSWORD": "hunter2", "API_TOKEN": "sk-secret"},
		})
		assert.Equal(t, "alpacon exec --env=API_TOKEN --env=PGPASSWORD web-01 -- sudo reboot", got.Command)
		assert.NotContains(t, got.Command, "hunter2")
		assert.NotContains(t, got.Command, "sk-secret")
		// Caveat rides in Description so a machine consumer replaying Command isn't misled.
		assert.Contains(t, got.Description, "re-read from your shell")
	})
}
