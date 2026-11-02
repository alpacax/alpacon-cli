//go:build linux

package localsignal

import (
	"context"
	"os"
	"os/exec"
)

func platformHeadless() bool { return headless("linux", os.LookupEnv, false) }

func (platformNotifier) Notify(ctx context.Context, title, body string) error {
	tool, err := exec.LookPath("notify-send")
	if err != nil {
		return err
	}
	return exec.CommandContext(ctx, tool, "--app-name=Alpacon", "-u", "normal", "--", title, body).Run()
}
