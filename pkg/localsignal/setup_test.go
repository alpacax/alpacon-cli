package localsignal

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	NewNotifier = func() Notifier { return notifyFunc(func(context.Context, string, string, string) error { return nil }) }
	OpenTTY = func() (io.WriteCloser, error) { return nil, errors.New("no test terminal") }
	dir, err := os.MkdirTemp("", "signal-test-home-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", dir); err != nil {
		panic(err)
	}
	if err := os.Setenv("USERPROFILE", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestRealNotifierIsReplaced(t *testing.T) {
	t.Parallel()
	emitter := New(io.Discard, nil, nil)
	if _, real := emitter.notifier.(platformNotifier); real {
		t.Fatal("real notifier must not be used by tests")
	}
}
