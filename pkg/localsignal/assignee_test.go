package localsignal

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A command held for the session's assignee is that person's own to confirm, so
// the signal says so instead of sending them to wait for an approver.
func TestFormatAssigneeConfirmation(t *testing.T) {
	t.Parallel()
	f := Fields{Command: true, Argv0: "/usr/bin/sudo --password secret", Server: "my-server", Account: "alice", Session: "ses-1"}
	for _, tc := range []struct {
		name        string
		fields      Fields
		title, body string
	}{
		{
			"assignee command",
			Fields{Command: true, Argv0: f.Argv0, Server: f.Server, Account: f.Account, Session: f.Session, Assignee: true},
			"Alpacon: confirmation pending",
			"sudo on my-server as alice needs your own confirmation. Session ses-1. Confirm or reject it in the Alpacon web console.",
		},
		{
			"approver command",
			f,
			"Alpacon: approval pending",
			"sudo on my-server as alice is waiting for approval. Session ses-1. Open the approval or wait for an approver.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			title, body := Format(ApprovalPending, tc.fields, 0)
			assert.Equal(t, tc.title, title)
			assert.Equal(t, tc.body, body)
			assert.NotContains(t, body, "secret")
			_, folded := Format(ApprovalPending, tc.fields, 2)
			assert.Equal(t, body+" +2 more.", folded)
		})
	}
}

// The assignee wording changes only the text: the signal keeps the approval
// pending kind, so it shares that kind's rate limit and console link.
func TestAssigneeConfirmationSharesTheApprovalLimitAndLink(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	now := time.Now()
	path := filepath.Join(t.TempDir(), "state", "signals.json")
	emitter := New(&output, nil, nil)
	emitter.WorkspaceURL = "https://console.example"
	emitter.statePath = func() (string, error) { return path, nil }
	emitter.headless = func() bool { return false }
	emitter.now = func() time.Time { return now }
	var notified []string
	emitter.notifier = notifyFunc(func(_ context.Context, title, body, link string) error {
		notified = append(notified, title+"|"+body+"|"+link)
		return nil
	})
	const link = "https://console.example/commands/cmd-1"
	emitter.Emit(ApprovalPending, Fields{Command: true, Argv0: "id", Session: "ses-1", ConsoleURL: link})
	emitter.Emit(ApprovalPending, Fields{Command: true, Argv0: "id", Session: "ses-1", ConsoleURL: link, Assignee: true})

	assert.Equal(t, []string{
		"Alpacon: approval pending|id is waiting for approval. Session ses-1. Open the approval or wait for an approver. " + link + "|" + link,
	}, notified, "the second signal of the same kind inside the window is folded")
	assert.Contains(t, output.String(), "==== ALPACON: CONFIRMATION PENDING ====\nid needs your own confirmation. Session ses-1. Confirm or reject it in the Alpacon web console.\n"+link+"\n")
}

// Ordinary server, account and session identifiers leave room for the console
// link on both lanes: the banner and the notification always carry it.
func TestHeldCommandSignalKeepsConsoleLinkWithOrdinaryIdentifiers(t *testing.T) {
	t.Parallel()
	const (
		workspace = "https://my-workspace.ap1.alpacon.io"
		session   = "550e8400-e29b-41d4-a716-446655440000"
		link      = workspace + "/commands/6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	)
	for _, tc := range []struct {
		name     string
		assignee bool
	}{
		{"assignee", true},
		{"approver", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			emitter := New(&output, nil, nil)
			emitter.WorkspaceURL = workspace
			path := filepath.Join(t.TempDir(), "state", "signals.json")
			emitter.statePath = func() (string, error) { return path, nil }
			emitter.headless = func() bool { return false }
			var notifiedBody, notifiedLink string
			emitter.notifier = notifyFunc(func(_ context.Context, _, body, link string) error {
				notifiedBody, notifiedLink = body, link
				return nil
			})
			emitter.Emit(ApprovalPending, Fields{
				Command: true, Argv0: "sudo", Server: "prod-web-01", Account: "ubuntu",
				Session: session, ConsoleURL: link, Assignee: tc.assignee,
			})
			assert.Contains(t, output.String(), "\n"+link+"\n")
			assert.Equal(t, link, notifiedLink)
			assert.Contains(t, notifiedBody, " "+link)
		})
	}
}
