//go:build darwin

package localsignal

import "context"

func platformNotificationArguments(ctx context.Context, title, body, _ string) []string {
	return darwinNotificationCommand(ctx, title, body).Args
}
