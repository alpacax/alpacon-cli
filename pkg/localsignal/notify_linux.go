//go:build linux

package localsignal

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

func platformHeadless() bool { return headless("linux", os.LookupEnv, false) }

func (platformNotifier) Notify(ctx context.Context, title, body, consoleURL string) error {
	tool, err := exec.LookPath("notify-send")
	if err != nil {
		return err
	}
	return linuxNotificationCommand(ctx, tool, title, body).Run()
}

func linuxNotificationCommand(ctx context.Context, tool, title, body string) *exec.Cmd {
	return exec.CommandContext(ctx, tool, "--app-name=Alpacon", "-u", "normal", "--", escapeMarkup(clean(title)), escapeMarkup(clean(body)))
}

func escapeMarkup(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
