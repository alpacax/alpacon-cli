//go:build !darwin && !linux && !windows

package localsignal

import "context"

func platformNotificationArguments(_ context.Context, title, body, link string) []string {
	return []string{title, body, link}
}
