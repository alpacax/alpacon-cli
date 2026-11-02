package localsignal

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type notifyFunc func(context.Context, string, string) error

func (f notifyFunc) Notify(ctx context.Context, title, body string) error { return f(ctx, title, body) }

func TestFormat(t *testing.T) {
	t.Parallel()
	f := Fields{Argv0: "/usr/bin/sudo --password secret", Server: "my-server", Account: "alice", Gate: "risk", Session: "ses-1"}
	for _, tc := range []struct {
		name        string
		kind        Kind
		fields      Fields
		title, body string
	}{
		{"denied", Denied, f, "Alpacon: command denied", "sudo on my-server as alice was refused by the risk gate and did not run. Session ses-1."},
		{"pending command", ApprovalPending, f, "Alpacon: approval pending", "sudo on my-server as alice is waiting for approval. Session ses-1. Open the approval or wait for an approver."},
		{"pending session", ApprovalPending, Fields{Session: "ses-1"}, "Alpacon: approval pending", "Work Session ses-1 is waiting for approval. Open the approval or wait for an approver."},
		{"paused", Paused, f, "Alpacon: session paused", "Work Session ses-1 is paused after a critical denial. The owner or an approver can resume or end it."},
		{"unknown fields", Denied, Fields{}, "Alpacon: command denied", "Command was refused and did not run."},
		{"unknown session", Paused, Fields{}, "Alpacon: session paused", "Work Session is paused after a critical denial. The owner or an approver can resume or end it."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			title, body := Format(tc.kind, tc.fields, 0)
			assert.Equal(t, tc.title, title)
			assert.Equal(t, tc.body, body)
			assert.NotContains(t, body, "secret")
			assert.NotContains(t, body, "--password")
			_, folded := Format(tc.kind, tc.fields, 3)
			assert.Equal(t, body+" +3 more.", folded)
		})
	}
}

func TestArgv0(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, command, want string }{
		{"unix", "/usr/bin/sudo cat secret", "sudo"},
		{"windows", `C:\Windows\System32\whoami.exe /all`, "whoami.exe"},
		{"quoted path", `"/some path/my-tool" --secret value`, "my-tool"},
		{"single quotes", `'/some path/my-tool' --secret value`, "my-tool"},
		{"whitespace", " \t sudo\nsecret", "sudo"},
		{"empty", "", "."},
	} {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); assert.Equal(t, tc.want, Argv0(tc.command)) })
	}
}

func TestHeadless(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, platform string
		env            map[string]string
		service, want  bool
	}{
		{"mac agent with no tty", "darwin", nil, false, false},
		{"ci", "darwin", map[string]string{"CI": "false"}, false, true},
		{"empty ssh connection", "darwin", map[string]string{"SSH_CONNECTION": ""}, false, true},
		{"empty ssh tty", "darwin", map[string]string{"SSH_TTY": ""}, false, true},
		{"ssh connection", "darwin", map[string]string{"SSH_CONNECTION": "remote"}, false, true},
		{"ssh tty", "windows", map[string]string{"SSH_TTY": "remote"}, false, true},
		{"linux no display", "linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "bus"}, false, true},
		{"linux no bus", "linux", map[string]string{"DISPLAY": ":0"}, false, true},
		{"linux x11", "linux", map[string]string{"DISPLAY": ":0", "DBUS_SESSION_BUS_ADDRESS": "bus"}, false, false},
		{"linux wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DBUS_SESSION_BUS_ADDRESS": "bus"}, false, false},
		{"windows service", "windows", nil, true, true},
		{"windows desktop", "windows", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, headless(tc.platform, func(key string) (string, bool) { value, ok := tc.env[key]; return value, ok }, tc.service))
		})
	}
}

func TestEmitHeadlessBannerAndSharedLimit(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	now := time.Now()
	path := filepath.Join(t.TempDir(), "state", "signals.json")
	emitter := New(&output, nil, nil)
	emitter.statePath = func() (string, error) { return path, nil }
	emitter.headless = func() bool { return true }
	emitter.now = func() time.Time { return now }
	emitter.notifier = notifyFunc(func(context.Context, string, string) error { t.Error("headless notifier called"); return nil })
	f := Fields{Argv0: "/usr/bin/id -secret", Session: "ses-1"}
	emitter.Emit(Denied, f)
	assert.Equal(t, "==== ALPACON: COMMAND DENIED ====\nid was refused and did not run. Session ses-1.\n================================\n", output.String())
	output.Reset()
	emitter.Emit(Denied, f)
	assert.Empty(t, output.String())
	now = now.Add(60 * time.Second)
	emitter.Emit(Denied, f)
	assert.Contains(t, output.String(), "Session ses-1. +1 more.")
}

func TestEmitNotifierFailureAndTimeout(t *testing.T) {
	// The fake clock pins the upper bound without a real notification or a two-second sleep.
	for _, mode := range []string{"error", "timeout", "success"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var output bytes.Buffer
				var debug []string
				emitter := New(&output, nil, func(message string, _ ...any) { debug = append(debug, message) })
				emitter.statePath = func() (string, error) { return "", errors.New("unreadable") }
				emitter.headless = func() bool { return false }
				finished := make(chan struct{})
				emitter.notifier = notifyFunc(func(ctx context.Context, title, body string) error {
					defer close(finished)
					assert.Equal(t, "Alpacon: session paused", title)
					assert.Contains(t, body, "Work Session ses-1 is paused")
					deadline, ok := ctx.Deadline()
					assert.True(t, ok, "notification context must have a deadline")
					assert.Equal(t, notifyTimeout, time.Until(deadline))
					switch mode {
					case "error":
						return errors.New("notifier failed")
					case "timeout":
						<-ctx.Done()
						return ctx.Err()
					default:
						return nil
					}
				})
				start := time.Now()
				emitter.Emit(Paused, Fields{Session: "ses-1"})
				assert.LessOrEqual(t, time.Since(start), notifyTimeout)
				<-finished
				assert.Contains(t, output.String(), "==== ALPACON: SESSION PAUSED ====\n")
				assert.NotEmpty(t, debug)
			})
		})
	}
}

func TestNoOptOut(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{"CI": true, "SSH_CONNECTION": true, "SSH_TTY": true, "DISPLAY": true, "WAYLAND_DISPLAY": true, "DBUS_SESSION_BUS_ADDRESS": true}
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		require.NoError(t, err)
		ast.Inspect(source, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				name := ""
				switch f := call.Fun.(type) {
				case *ast.Ident:
					name = f.Name
				case *ast.SelectorExpr:
					name = f.Sel.Name
				}
				if name == "getenv" || name == "Getenv" || name == "LookupEnv" || name == "lookupEnv" {
					require.Len(t, call.Args, 1)
					literal, ok := call.Args[0].(*ast.BasicLit)
					require.True(t, ok, "environment reads must use literal keys in %s", file)
					key, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					assert.Contains(t, allowed, key)
				}
			}
			return true
		})
		for _, imp := range source.Imports {
			assert.NotContains(t, imp.Path.Value, "cobra")
			assert.NotContains(t, imp.Path.Value, "pflag")
			assert.NotContains(t, imp.Path.Value, `alpacon-cli/config"`)
		}
	}
}

func TestDefaultStatePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	emitter := New(&bytes.Buffer{}, nil, nil)
	path, err := emitter.statePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(os.Getenv("HOME"), ".alpacon", "state", "signals.json"), path)
}
