//go:build linux

package localsignal

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

func platformHeadless() bool { return false }

func (platformNotifier) Notify(ctx context.Context, title, body, consoleURL string) error {
	tool := linuxNotifierPath()
	if tool == "" {
		return nil
	}
	return linuxNotificationCommand(ctx, tool, title, body).Run()
}

func linuxNotificationCommand(ctx context.Context, tool, title, body string) *exec.Cmd {
	return exec.CommandContext(ctx, tool, "--app-name=Alpacon", "-u", "normal", "--", escapeMarkup(clean(title)), escapeMarkup(clean(body)))
}

func escapeMarkup(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func linuxNotifierPath() string {
	for _, tool := range []string{"/usr/bin/notify-send", "/bin/notify-send"} {
		if info, err := os.Stat(tool); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return tool
		}
	}
	return ""
}
