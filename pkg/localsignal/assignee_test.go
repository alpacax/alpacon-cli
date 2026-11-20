package localsignal

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// Long identifiers make the body yield to the console link: the body text is
// trimmed to fit and the fold suffix stays.
func TestHeldCommandSignalTrimsBodyToKeepConsoleLink(t *testing.T) {
	t.Parallel()
	const (
		workspace = "https://my-workspace.ap1.alpacon.io"
		session   = "550e8400-e29b-41d4-a716-446655440000"
		link      = workspace + "/commands/6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	)
	for _, tc := range []struct {
		name     string
		assignee bool
		folded   int
	}{
		{"assignee", true, 0},
		{"assignee folded", true, 12},
		{"approver", false, 0},
		{"approver folded", false, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fields := Fields{
				Command: true, Argv0: "systemctl", Server: "production-web-server-0001.ap-northeast-2", Account: "www-data",
				Session: session, ConsoleURL: link, Assignee: tc.assignee,
			}
			_, full := Format(ApprovalPending, fields, 0)
			require.Greater(t, utf8.RuneCountInString(full)+1+len(link), 256, "every row must reach the trim path: %q", full)
			bannerBody, notifyBody, notifyLink := emitHeld(t, workspace, fields, tc.folded)
			suffix := ""
			if tc.folded > 0 {
				suffix = " +" + strconv.Itoa(tc.folded) + " more."
			}

			text, found := strings.CutSuffix(bannerBody, "\n"+link)
			require.True(t, found, "banner keeps the link: %q", bannerBody)
			assert.NotEmpty(t, text)
			assert.True(t, strings.HasPrefix(full, text), "banner text must prefix the body: %q", text)
			assert.Equal(t, 256, utf8.RuneCountInString(bannerBody))

			assert.Equal(t, link, notifyLink)
			text, found = strings.CutSuffix(notifyBody, suffix+" "+link)
			require.True(t, found, "notification keeps the fold suffix and the link: %q", notifyBody)
			assert.NotEmpty(t, text)
			assert.True(t, strings.HasPrefix(full, text), "notification text must prefix the body: %q", text)
			assert.Equal(t, 256, utf8.RuneCountInString(notifyBody))
		})
	}
}

// A link that would leave no body text is dropped; one rune shorter, it is kept
// beside a single rune of body text. The fold suffix is on the notification only,
// so near the limit the banner keeps a link the notification drops.
func TestConsoleLinkDroppedOnlyWhenNoBodyTextRemains(t *testing.T) {
	t.Parallel()
	const workspace = "https://console.example"
	fields := Fields{Command: true, Argv0: "id", Session: "ses-1"}
	_, full := Format(ApprovalPending, fields, 0)
	// bannerText and notifyText are the body runes kept beside the link; -1 means
	// the link is dropped.
	for _, tc := range []struct {
		name                   string
		size, folded           int
		bannerText, notifyText int
	}{
		{"leaves no body text", 255, 0, -1, -1},
		{"leaves one rune", 254, 0, 1, 1},
		{"fold leaves no notification text", 245, 12, 10, -1},
		{"fold leaves one notification rune", 244, 12, 11, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := fields
			f.ConsoleURL = workspace + "/" + strings.Repeat("x", tc.size-len(workspace)-1)
			suffix := ""
			if tc.folded > 0 {
				suffix = " +" + strconv.Itoa(tc.folded) + " more."
			}
			wantBanner := full
			if tc.bannerText >= 0 {
				wantBanner = full[:tc.bannerText] + "\n" + f.ConsoleURL
			}
			wantNotify, wantLink := full+suffix, ""
			if tc.notifyText >= 0 {
				wantNotify, wantLink = full[:tc.notifyText]+suffix+" "+f.ConsoleURL, f.ConsoleURL
			}
			bannerBody, notifyBody, notifyLink := emitHeld(t, workspace, f, tc.folded)
			assert.Equal(t, wantBanner, bannerBody)
			assert.Equal(t, wantNotify, notifyBody)
			assert.Equal(t, wantLink, notifyLink)
		})
	}
}

// emitHeld emits one approval pending signal, seeding a fold of folded repeats,
// and returns the banner body, the notification body and the notification link.
func emitHeld(t *testing.T, workspace string, fields Fields, folded int) (string, string, string) {
	t.Helper()
	var output bytes.Buffer
	emitter := New(&output, nil, nil)
	emitter.WorkspaceURL = workspace
	now := time.Unix(1700000000, 0)
	path := filepath.Join(t.TempDir(), "signals.json")
	if folded > 0 {
		host := strings.TrimPrefix(workspace, "https://")
		data, err := json.Marshal(map[string]entry{
			host + "|" + string(ApprovalPending) + ":" + fields.Session: {Last: now.Add(-time.Hour), Folded: folded},
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0600))
	}
	emitter.statePath = func() (string, error) { return path, nil }
	emitter.now = func() time.Time { return now }
	emitter.headless = func() bool { return false }
	calls := 0
	var notifyBody, notifyLink string
	emitter.notifier = notifyFunc(func(_ context.Context, _, body, link string) error {
		calls++
		notifyBody, notifyLink = body, link
		return nil
	})
	emitter.Emit(ApprovalPending, fields)
	require.Equal(t, 1, calls)
	lines := strings.SplitN(output.String(), "\n", 2)
	bannerBody, found := strings.CutSuffix(lines[1], "\n================================\n")
	require.True(t, found, "banner frame: %q", output.String())
	return bannerBody, notifyBody, notifyLink
}
