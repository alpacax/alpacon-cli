//go:build darwin

package localsignal

import (
	"context"
	"os"
	"os/exec"
)

func platformHeadless() bool { return headless("darwin", os.LookupEnv, false) }

func (platformNotifier) Notify(ctx context.Context, title, body string) error {
	const script = "on run argv\n display notification (item 2 of argv) with title (item 1 of argv)\nend run"
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, "--", title, body).Run()
}
