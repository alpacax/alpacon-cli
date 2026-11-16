//go:build darwin

package localsignal

import (
	"context"
	"os/exec"
)

func platformHeadless() bool { return false }

func (platformNotifier) Notify(ctx context.Context, title, body, consoleURL string) error {
	return darwinNotificationCommand(ctx, title, body).Run()
}

func darwinNotificationCommand(ctx context.Context, title, body string) *exec.Cmd {
	const script = "on run argv\n display notification (item 2 of argv) with title (item 1 of argv)\nend run"
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, "--", clean(title), clean(body))
}
