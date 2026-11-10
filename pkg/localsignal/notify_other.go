//go:build !darwin && !linux && !windows

package localsignal

import (
	"context"
)

func platformHeadless() bool { return false }

func (platformNotifier) Notify(context.Context, string, string, string) error { return nil }
