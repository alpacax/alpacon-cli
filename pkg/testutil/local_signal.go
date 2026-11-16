package testutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/localsignal"
)

type silentNotifier struct{}

func (silentNotifier) Notify(context.Context, string, string, string) error { return nil }

func LocalSignalTestMain(m *testing.M) {
	localsignal.NewNotifier = func() localsignal.Notifier { return silentNotifier{} }
	localsignal.OpenTTY = func() (io.WriteCloser, error) { return nil, errors.New("no test terminal") }
	// Helper processes inherit the parent's isolated home, including an explicit per-test home.
	dir := ""
	if os.Getenv("ALPACON_TEST_HOME") == "" {
		var err error
		dir, err = os.MkdirTemp("", "alpacon-test-home-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for key, value := range map[string]string{"HOME": dir, "USERPROFILE": dir, "ALPACON_TEST_HOME": dir} {
			if err := os.Setenv(key, value); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
	code := m.Run()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}
