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
		assert.Contains(t, hint, "work-session update")
		// Adding a policy is an expansion, so the server queues it for approval
		// (sudo/services.py classify_sudo_policies_split). "Add it and re-run"
		// alone would read as immediate.
		assert.Contains(t, hint, "may require approval")
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
		assert.Contains(t, hint, "may require approval")
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
		assert.Contains(t, hint, "may need an approval of its own")
		assert.NotContains(t, hint, `--description "`, "a queued edit must not read as part of the reviewer-free command")
	})

	t.Run("workspace-mfa-disabled points at the workspace setting, not the session", func(t *testing.T) {
		// Checked before the branch split (sudo/services.py
		// handle_sudo_approval_request), so no work session or policy can lift it.
		hint := sudoDenialHint("Alpacon denied this sudo command (WORKSPACE_SUDO_WITH_MFA_DISABLED).\n")
		assert.Contains(t, hint, "workspace access-control update")
		assert.NotContains(t, hint, "work-session update", "no session edit lifts a workspace-wide denial")
	})

	t.Run("session-missing reads as retryable, not as a policy problem", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_SESSION_MISSING).\n")
		assert.Contains(t, hint, "Re-run")
		assert.NotContains(t, hint, "work-session update", "nothing about the session's policies is wrong here")
	})

	t.Run("scope-not-allowed names the sudo scope and the replace caveat", func(t *testing.T) {
		// --scope replaces the whole list, so guidance that omits the caveat
		// would have the user silently drop every other scope.
		hint := sudoDenialHint("Alpacon denied this sudo command (WORK_SESSION_SCOPE_NOT_ALLOWED).\n")
		assert.Contains(t, hint, "'sudo' scope")
		assert.Contains(t, hint, "work-session update [SESSION_ID] --scope")
		assert.Contains(t, hint, "replaces the whole list")
	})

	t.Run("command-not-authorized names the accountable-user requirement", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_COMMAND_NOT_AUTHORIZED).\n")
		assert.Contains(t, hint, "accountable user")
		assert.Contains(t, hint, "personal API token")
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
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_COMMAND_NOT_AUTHORIZED).\n"))
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (WORKSPACE_SUDO_WITH_MFA_DISABLED).\n"))
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (WORK_SESSION_SCOPE_NOT_ALLOWED).\n"))
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_SESSION_MISSING).\n"))
		// An unknown code gets a fallback hint but must never enter the pending
		// path: the CLI cannot know an approval request exists behind it.
		assert.False(t, hasSudoApprovalDenial(
			"Alpacon denied this sudo command (SUDO_BRAND_NEW_CODE).\n"))
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

func TestPendingSudoDenial(t *testing.T) {
	t.Run("answers with the pending code, not an earlier terminal one", func(t *testing.T) {
		// One command line can run several sudo calls and carry a denial line for
		// each. The pending path classifies on the pendingApproval codes alone, so
		// its hint must come from the code that made it pending—here the intent
		// deviation, not the terminal no-policy denial that sits earlier in the
		// table and is what sudoDenialHint returns for the same output.
		out := "Alpacon denied this sudo command (SUDO_NO_WORKSESSION_POLICY).\n" +
			"Alpacon denied this sudo command (SUDO_INTENT_DEVIATION).\n"
		hint, pending := pendingSudoDenial(out)
		assert.True(t, pending, "an intent deviation is a pending approval: %q", out)
		assert.Contains(t, hint, "--title")
		assert.Contains(t, sudoDenialHint(out), "--sudo")
	})

	t.Run("prefers the self-service code over another pending one", func(t *testing.T) {
		// Both codes classify the same, so answering with the one that has no way
		// past the wait would drop the only guidance the user can act on. The
		// approval-required entry sits earlier in the table, so a scan that took
		// the first pending match would lose the hint on this output.
		out := "Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n" +
			"Alpacon denied this sudo command (SUDO_INTENT_DEVIATION).\n"
		hint, pending := pendingSudoDenial(out)
		assert.True(t, pending)
		assert.Contains(t, hint, "--title")
	})

	t.Run("withholds the hint for a code whose only way out is the wait", func(t *testing.T) {
		// HandlePendingApproval's own message already says "re-run after
		// approval", so returning this entry's guidance would print it twice.
		hint, pending := pendingSudoDenial(
			"Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n")
		assert.True(t, pending, "SUDO_APPROVAL_REQUIRED must still classify as pending")
		assert.Empty(t, hint)
	})

	t.Run("not pending when the output carries no pending code", func(t *testing.T) {
		for _, out := range []string{
			"Alpacon denied this sudo command (SUDO_RISK_DENIED).\n",
			"",
		} {
			hint, pending := pendingSudoDenial(out)
			assert.False(t, pending, "output must not classify as pending: %q", out)
			assert.Empty(t, hint)
		}
	})
}

func TestSudoDenialHintFallsBackToTheRawCode(t *testing.T) {
	// The server adds denial codes on its own release train and nothing enforces
	// this table's sync with it, so an unknown code must still leave the user
	// something to act on instead of a bare denial line.
	t.Run("names a code the table does not carry", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_BRAND_NEW_CODE).\n")
		assert.Contains(t, hint, "SUDO_BRAND_NEW_CODE")
		assert.Contains(t, hint, "Alpacon console")
	})

	t.Run("a known code still gets its own guidance", func(t *testing.T) {
		hint := sudoDenialHint("Alpacon denied this sudo command (SUDO_RISK_DENIED).\n")
		assert.Contains(t, hint, "risk")
		assert.NotContains(t, hint, "no guidance for that code")
	})

	t.Run("no hint without the anchored denial line", func(t *testing.T) {
		assert.Empty(t, sudoDenialHint("build finished (SUDO_BRAND_NEW_CODE).\n"))
		assert.Empty(t, sudoDenialHint("Alpacon denied this sudo command.\n"))
		assert.Empty(t, sudoDenialHint(""))
	})

	t.Run("a code outside the sanitizer's shape is not echoed", func(t *testing.T) {
		// alpacon_approval.c only ever emits [A-Z0-9_]; anything else in that
		// slot came from the command's own output, so it must not be printed.
		for _, forged := range []string{
			"Alpacon denied this sudo command (\x1b[31mSUDO_FAKE).\n",
			"Alpacon denied this sudo command (sudo_lowercase).\n",
			"Alpacon denied this sudo command (SUDO FAKE).\n",
			"Alpacon denied this sudo command ().\n",
		} {
			assert.Empty(t, sudoDenialHint(forged), "forged code must not reach the hint: %q", forged)
		}
	})

	t.Run("a code longer than the plugin's buffer is not echoed", func(t *testing.T) {
		// The 63-char cap mirrors alpacon_approval.c's char[64], so it is the
		// bound most likely to drift silently: a longer code has the sanitizer's
		// own shape and would otherwise be echoed verbatim.
		const line = "Alpacon denied this sudo command (%s).\n"
		atCap := strings.Repeat("A", 63)
		assert.Contains(t, sudoDenialHint(fmt.Sprintf(line, atCap)), atCap)
		assert.Empty(t, sudoDenialHint(fmt.Sprintf(line, strings.Repeat("A", 64))))
	})

	t.Run("a look-alike line does not suppress the real denial after it", func(t *testing.T) {
		hint := sudoDenialHint(
			"Alpacon denied this sudo command (not a code).\n" +
				"Alpacon denied this sudo command (SUDO_BRAND_NEW_CODE).\n")
		assert.Contains(t, hint, "SUDO_BRAND_NEW_CODE")
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
