//go:build !darwin && !linux && !windows

package localsignal

import (
	"context"
	"os"
	"runtime"
)

func platformHeadless() bool { return headless(runtime.GOOS, os.LookupEnv, false) }

func (platformNotifier) Notify(context.Context, string, string, string) error { return nil }
