package exec

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/stretchr/testify/assert"
)

// denialLine renders the plugin's terminal denial line for code. It spells the
// prefix out rather than reusing sudoDenialLinePrefix, so a change to that
// constant fails these tests instead of moving along with them.
func denialLine(code string) string {
	return "Alpacon denied this sudo command (" + code + ").\n"
}

func TestSudoDenialHint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		code            string
		wantContains    []string
		wantNotContains []string
	}{
		{
			// Adding a policy is an expansion, so the server queues it for approval
			// (sudo/services.py classify_sudo_policies_split). "Add it and re-run"
			// alone would read as immediate.
			name:         "no-policy names the policy edit and its own approval",
			code:         "SUDO_NO_WORKSESSION_POLICY",
			wantContains: []string{"work-session update", "may require approval"},
		},
		{
			name:         "presence-required points to a step-up",
			code:         sudoPresenceRequiredCode,
			wantContains: []string{"step-up"},
		},
		{
			name:         "approval-required points to re-running after approval",
			code:         "SUDO_APPROVAL_REQUIRED",
			wantContains: []string{"approv"},
		},
		{
			name:         "policy-mfa-required names MFA as what blocks the covered command",
			code:         "SUDO_POLICY_MFA_REQUIRED",
			wantContains: []string{"MFA", "work-session update", "may require approval"},
		},
		{
			// The AI judges the command against both the session title and its
			// description (WorkSessionRiskContext), but only a title edit applies at
			// once: a description edit on an approved/active session is queued for an
			// approval of its own, which is the wait this path exists to skip.
			name: "intent-deviation offers only the edit that skips the wait",
			code: "SUDO_INTENT_DEVIATION",
			wantContains: []string{
				"approval request",
				"work-session update [SESSION_ID] --title",
				"may need an approval of its own",
			},
			// A queued edit must not read as part of the reviewer-free command.
			wantNotContains: []string{`--description "`},
		},
		{
			// Checked before the branch split (sudo/services.py
			// handle_sudo_approval_request), so no work session or policy lifts it.
			name:            "workspace-mfa-disabled points at the workspace setting, not the session",
			code:            "WORKSPACE_SUDO_WITH_MFA_DISABLED",
			wantContains:    []string{"workspace access-control update"},
			wantNotContains: []string{"work-session update"},
		},
		{
			name:            "session-missing reads as retryable, not as a policy problem",
			code:            "SUDO_SESSION_MISSING",
			wantContains:    []string{"Re-run"},
			wantNotContains: []string{"work-session update"},
		},
		{
			// --scope replaces the whole list, so guidance that omits the caveat
			// would have the user silently drop every other scope.
			name:         "scope-not-allowed names the sudo scope and the replace caveat",
			code:         "WORK_SESSION_SCOPE_NOT_ALLOWED",
			wantContains: []string{"'sudo' scope", "work-session update [SESSION_ID] --scope", "replaces the whole list"},
		},
		{
			name:         "command-not-authorized names the accountable-user requirement",
			code:         "SUDO_COMMAND_NOT_AUTHORIZED",
			wantContains: []string{"accountable user", "personal API token"},
		},
		{
			name:         "risk-denied is a terminal denial",
			code:         "SUDO_RISK_DENIED",
			wantContains: []string{"risk"},
			// Disclosure: never echo a score/reasoning, only the category.
			wantNotContains: []string{"score"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := sudoDenialHint(denialLine(tt.code))
			for _, want := range tt.wantContains {
				assert.Contains(t, hint, want)
			}
			for _, unwanted := range tt.wantNotContains {
				assert.NotContains(t, hint, unwanted)
			}
			assert.NotContains(t, hint, "no guidance for that code", "a table code must not reach the unknown-code fallback")
		})
	}
}

// TestSudoDenialHintSeparatesTheTwoPolicyDenials pins that a missing policy and
// a policy that demands MFA do not answer with the same text. Both point at
// `work-session update --sudo`, so identical wording would leave the user unable
// to tell which of the two they hit.
func TestSudoDenialHintSeparatesTheTwoPolicyDenials(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t,
		sudoDenialHint(denialLine("SUDO_NO_WORKSESSION_POLICY")),
		sudoDenialHint(denialLine("SUDO_POLICY_MFA_REQUIRED")))
}

func TestHasSudoPresenceDenial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"the real presence denial line", denialLine(sudoPresenceRequiredCode), true},
		{
			// The denial line may be preceded by legitimate stdout; the detector
			// must still fire.
			name:   "buried in real command output",
			output: "reading config...\nApplying changes\n" + denialLine(sudoPresenceRequiredCode),
			want:   true,
		},
		{"another denial code", denialLine("SUDO_RISK_DENIED"), false},
		{"another denial code", denialLine("SUDO_APPROVAL_REQUIRED"), false},
		{"clean output", "ok\n", false},
		{"empty output", "", false},
		{
			// A command whose own output prints the bare token, without the plugin's
			// denial line, must not be mistaken for a presence denial.
			name:   "forged parenthesized token",
			output: "echo \"(SUDO_PRESENCE_REQUIRED)\"\n(SUDO_PRESENCE_REQUIRED)\n",
			want:   false,
		},
		{
			// The matcher anchors on the plugin's exact line, which ends in a period.
			// A line that stops at ")" is not the plugin's output.
			name:   "denial line without the trailing period",
			output: "Alpacon denied this sudo command (SUDO_PRESENCE_REQUIRED)\n",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasSudoPresenceDenial(tt.output), "output: %q", tt.output)
		})
	}
}

func TestHasSudoApprovalDenial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"the real approval denial line", denialLine("SUDO_APPROVAL_REQUIRED"), true},
		{
			// The server takes the same HITL branch and only swaps the code
			// (sudo/services.py), so an approval request is in flight here too.
			name:   "an intent-deviation denial",
			output: denialLine("SUDO_INTENT_DEVIATION"),
			want:   true,
		},
		{"presence-required", denialLine(sudoPresenceRequiredCode), false},
		{"risk-denied", denialLine("SUDO_RISK_DENIED"), false},
		{"policy-mfa-required", denialLine("SUDO_POLICY_MFA_REQUIRED"), false},
		{"command-not-authorized", denialLine("SUDO_COMMAND_NOT_AUTHORIZED"), false},
		{"workspace-mfa-disabled", denialLine("WORKSPACE_SUDO_WITH_MFA_DISABLED"), false},
		{"scope-not-allowed", denialLine("WORK_SESSION_SCOPE_NOT_ALLOWED"), false},
		{"session-missing", denialLine("SUDO_SESSION_MISSING"), false},
		{
			// An unknown code gets a fallback hint but must never enter the pending
			// path: the CLI cannot know an approval request exists behind it.
			name:   "an unknown code",
			output: denialLine("SUDO_BRAND_NEW_CODE"),
			want:   false,
		},
		{
			// A command whose own output prints the token, without the plugin's
			// denial line, must not be mistaken for a pending approval.
			name:   "forged parenthesized token",
			output: "echo \"(SUDO_APPROVAL_REQUIRED)\"\n(SUDO_APPROVAL_REQUIRED)\n",
			want:   false,
		},
		{"clean output", "ok\n", false},
		{"empty output", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasSudoApprovalDenial(tt.output), "output: %q", tt.output)
		})
	}
}

func TestPendingSudoDenial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		output           string
		wantPending      bool
		wantHintContains string // "" means the hint must be withheld
	}{
		{
			// One command line can run several sudo calls and carry a denial line for
			// each. The pending path classifies on the pendingApproval codes alone, so
			// its hint must come from the code that made it pending—not from the
			// terminal no-policy denial that sits earlier in the table.
			name:             "answers with the pending code, not an earlier terminal one",
			output:           denialLine("SUDO_NO_WORKSESSION_POLICY") + denialLine("SUDO_INTENT_DEVIATION"),
			wantPending:      true,
			wantHintContains: "--title",
		},
		{
			// Both codes classify the same, so answering with the one that has no way
			// past the wait would drop the only guidance the user can act on. The
			// approval-required entry sits earlier in the table, so a scan that took
			// the first pending match would lose the hint on this output.
			name:             "prefers the self-service code over another pending one",
			output:           denialLine("SUDO_APPROVAL_REQUIRED") + denialLine("SUDO_INTENT_DEVIATION"),
			wantPending:      true,
			wantHintContains: "--title",
		},
		{
			// HandlePendingApproval's own message already says "re-run after
			// approval", so returning this entry's guidance would print it twice.
			name:        "withholds the hint for a code whose only way out is the wait",
			output:      denialLine("SUDO_APPROVAL_REQUIRED"),
			wantPending: true,
		},
		{"a terminal code is not pending", denialLine("SUDO_RISK_DENIED"), false, ""},
		// Claiming pending on a code the table does not carry would tell a script to
		// retry what the server may have refused outright, so drift must fail terminal.
		{"an unknown code is not pending", denialLine("SUDO_BRAND_NEW_CODE"), false, ""},
		{"empty output is not pending", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint, pending := pendingSudoDenial(tt.output)
			assert.Equal(t, tt.wantPending, pending, "output: %q", tt.output)
			if tt.wantHintContains == "" {
				assert.Empty(t, hint)
				return
			}
			assert.Contains(t, hint, tt.wantHintContains)
		})
	}

	t.Run("the general hint still answers with the earlier terminal code", func(t *testing.T) {
		// sudoDenialHint and pendingSudoDenial scan the same table by different
		// rules, so the same output must be able to produce two different hints.
		out := denialLine("SUDO_NO_WORKSESSION_POLICY") + denialLine("SUDO_INTENT_DEVIATION")
		assert.Contains(t, sudoDenialHint(out), "--sudo")
	})
}

func TestSudoDenialHintFallsBackToTheRawCode(t *testing.T) {
	t.Parallel()
	// The server adds denial codes on its own release train and nothing enforces
	// this table's sync with it, so an unknown code must still leave the user
	// something to act on instead of a bare denial line.
	tests := []struct {
		name         string
		output       string
		wantContains []string // empty means the output must produce no hint at all
	}{
		{
			name:         "names a code the table does not carry",
			output:       denialLine("SUDO_BRAND_NEW_CODE"),
			wantContains: []string{"SUDO_BRAND_NEW_CODE", "Alpacon console"},
		},
		{
			// A malformed slot must not suppress the hint for the real denial that
			// follows it.
			name:         "a look-alike line does not suppress the real denial after it",
			output:       denialLine("not a code") + denialLine("SUDO_BRAND_NEW_CODE"),
			wantContains: []string{"SUDO_BRAND_NEW_CODE"},
		},
		{"the code slot without the prefix", "build finished (SUDO_BRAND_NEW_CODE).\n", nil},
		{"empty output", "", nil},
		{
			// A command that merely prints the code, with no denial line at all.
			name:   "bare code in command output",
			output: "echo SUDO_RISK_DENIED\nSUDO_RISK_DENIED\n",
		},
		{
			name:   "forged parenthesized token",
			output: "echo \"(SUDO_RISK_DENIED)\"\n(SUDO_RISK_DENIED)\n",
		},
		// alpacon_approval.c only ever emits [A-Z0-9_]; anything else in that slot
		// came from the command's own output, so it must not be echoed back.
		{"escape sequence in the code slot", denialLine("\x1b[31mSUDO_FAKE"), nil},
		{"lowercase code", denialLine("sudo_lowercase"), nil},
		{"space in the code", denialLine("SUDO FAKE"), nil},
		// nil, not a coverage gap: the sanitizer drops a bad code whole, parentheses
		// included, so an empty slot is not a shape the agent ever emits.
		{"empty code slot", denialLine(""), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := sudoDenialHint(tt.output)
			if len(tt.wantContains) == 0 {
				assert.Empty(t, hint, "output: %q", tt.output)
				return
			}
			for _, want := range tt.wantContains {
				assert.Contains(t, hint, want)
			}
		})
	}

	t.Run("a code longer than the plugin's buffer is not echoed", func(t *testing.T) {
		// The 63-char cap mirrors alpacon_approval.c's char[64], so it is the bound
		// most likely to drift silently: a longer code has the sanitizer's own
		// shape and would otherwise be echoed verbatim.
		atCap := strings.Repeat("A", 63)
		assert.Contains(t, sudoDenialHint(denialLine(atCap)), atCap)
		assert.Empty(t, sudoDenialHint(denialLine(strings.Repeat("A", 64))))
	})
}

func TestSudoDenialHintCodelessDenial(t *testing.T) {
	t.Parallel()
	// Spelled out, not reused from sudoDenialCodelessLine, so changing that constant
	// fails these tests instead of moving along with them.
	const codelessLine = "Alpacon denied this sudo command.\n"

	tests := []struct {
		name            string
		output          string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "the codeless line points at the console",
			output:       codelessLine,
			wantContains: []string{"no code", "Alpacon console"},
		},
		{
			// Fires deliberately, not by accident—see sudoDenialHint's codeless
			// branch for why a forged match is accepted.
			name:         "a failed command that printed the sentence itself",
			output:       "echo \"Alpacon denied this sudo command.\"\n",
			wantContains: []string{"Alpacon console"},
		},
		{
			name:            "a code the table carries still wins on the same output",
			output:          denialLine("SUDO_RISK_DENIED") + codelessLine,
			wantContains:    []string{"runtime risk assessment"},
			wantNotContains: []string{"no code"},
		},
		{
			name:            "a code the table does not carry still wins on the same output",
			output:          denialLine("SUDO_BRAND_NEW_CODE") + codelessLine,
			wantContains:    []string{"SUDO_BRAND_NEW_CODE"},
			wantNotContains: []string{"no code"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := sudoDenialHint(tt.output)
			for _, want := range tt.wantContains {
				assert.Contains(t, hint, want)
			}
			for _, notWant := range tt.wantNotContains {
				assert.NotContains(t, hint, notWant)
			}
		})
	}
}

func TestIsApprovalDenial(t *testing.T) {
	t.Parallel()
	approvalDenial := &event.RemoteCommandError{ExitCode: 1, Output: denialLine("SUDO_APPROVAL_REQUIRED")}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"the denial line accompanies a non-zero exit", approvalDenial, true},
		{"wrapped RemoteCommandError", fmt.Errorf("failed to execute command: %w", approvalDenial), true},
		{
			name: "an intent-deviation denial",
			err:  &event.RemoteCommandError{ExitCode: 1, Output: denialLine("SUDO_INTENT_DEVIATION")},
			want: true,
		},
		{
			// err == nil means the command did not actually get denied; a command
			// that merely echoes the denial line must not be treated as pending.
			name: "the command printed the line but succeeded",
			err:  nil,
			want: false,
		},
		{
			name: "a non-approval denial",
			err:  &event.RemoteCommandError{ExitCode: 1, Output: denialLine("SUDO_RISK_DENIED")},
			want: false,
		},
		{"a plain error without the denial line", errors.New("nope"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isApprovalDenial(tt.err), "err: %v", tt.err)
		})
	}
}

func TestReRunHint(t *testing.T) {
	t.Parallel()
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
