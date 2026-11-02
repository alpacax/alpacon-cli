package localsignal

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
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

type notifyFunc func(context.Context, string, string, string) error

func (f notifyFunc) Notify(ctx context.Context, title, body, link string) error {
	return f(ctx, title, body, link)
}

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
		{"password assignment", "PGPASSWORD=hunter2 psql -h db", "psql"},
		{"access key assignment", "AWS_SECRET_ACCESS_KEY=abc123 aws s3 ls", "aws"},
		{"quoted executable secret", `"/opt/my tool/run --token=SECRET" x`, ""},
		{"assignment separator", "token=abc;curl x", ""},
		{"command separator", "echo;cat /etc/shadow", "echo"},
		{"sudo account", "/usr/bin/sudo -u x id", "sudo"},
		{"quoted assignment", `A="one two" B='three four' psql`, "psql"},
		{"invalid name", "tool=", ""},
		{"long name", strings.Repeat("x", 65), ""},
		{"windows", `C:\Windows\System32\whoami.exe /all`, ""},
		{"quoted path", `"/some path/my-tool" --secret value`, ""},
		{"single quotes", `'/some path/my-tool' --secret value`, ""},
		{"whitespace", " \t sudo\nsecret", "sudo"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Argv0(tc.command))
			title, body := Format(Denied, Fields{Argv0: tc.command}, 0)
			for _, secret := range []string{"=", "hunter2", "SECRET", "abc123"} {
				assert.NotContains(t, title+body, secret)
			}
		})
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
	emitter.notifier = notifyFunc(func(context.Context, string, string, string) error { t.Error("headless notifier called"); return nil })
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
				emitter.notifier = notifyFunc(func(ctx context.Context, title, body, _ string) error {
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
	// Include every direct caller and error sink, so an opt-out cannot move outside this package.
	var files []string
	err := filepath.WalkDir("../..", func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.Contains(file, filepath.Join("pkg", "testutil")) || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		source := string(data)
		if strings.Contains(file, filepath.Join("pkg", "localsignal")) || strings.Contains(source, `"github.com/alpacax/alpacon-cli/pkg/localsignal"`) {
			files = append(files, file)
			return nil
		}
		for _, call := range []string{"SignalRefusal(", "SignalPaused(", "SignalResponse(", "EmitLocalSignal(", "SignalApprovalPending(", "signalErrorsIn("} {
			if strings.Contains(source, call) {
				files = append(files, file)
				break
			}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, len(files), 15)
	for _, file := range files {
		source, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		require.NoError(t, err)
		ast.Inspect(source, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch f := call.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			}
			switch name {
			case "getenv", "Getenv", "LookupEnv", "lookupEnv", "GetString", "GetBool", "GetInt", "GetDuration":
				require.Len(t, call.Args, 1)
				if id, ok := call.Args[0].(*ast.Ident); ok {
					assert.Equal(t, "DebugEnvVar", id.Name)
					assert.Equal(t, "log.go", filepath.Base(file))
					return true
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				require.True(t, ok, "reads must name their key in %s", file)
				key, err := strconv.Unquote(literal.Value)
				require.NoError(t, err)
				normalCommandReads := map[string]bool{"editor": true, "username": true, "groupname": true, "force": true, "work-session": true, "recursive": true, "no-overwrite": true, "ALPACON_EDITOR": true, "VISUAL": true, "EDITOR": true}
				assert.True(t, allowed[key] || normalCommandReads[key], "unreviewed read %s in signal path %s", key, file)
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "config" {
					assert.Contains(t, []string{"LoadConfig", "ResolveAuthMethod"}, selector.Sel.Name, "unreviewed config read in %s", file)
				}
			}
			return true
		})
		if strings.Contains(file, filepath.Join("pkg", "localsignal")) {
			for _, imp := range source.Imports {
				assert.NotContains(t, imp.Path.Value, "cobra")
				assert.NotContains(t, imp.Path.Value, "pflag")
				assert.NotContains(t, imp.Path.Value, `alpacon-cli/config"`)
			}
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

type terminalBuffer struct {
	bytes.Buffer
	closed bool
}

func (b *terminalBuffer) Close() error { b.closed = true; return nil }

func TestBannerDestination(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		json, tty bool
	}{
		{"json with tty", true, true}, {"json without tty", true, false}, {"text", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			stderr.WriteString("existing stderr\n")
			tty := &terminalBuffer{}
			e := New(&stderr, nil, nil)
			e.WorkspaceURL = "https://console.example"
			e.JSON = tc.json
			e.statePath = func() (string, error) { return "", errors.New("no state") }
			e.openTTY = func() (io.WriteCloser, error) {
				if !tc.tty {
					return nil, errors.New("no controlling terminal")
				}
				return tty, nil
			}
			e.headless = func() bool { return false }
			notified := make(chan string, 1)
			e.notifier = notifyFunc(func(_ context.Context, _, body, link string) error {
				notified <- link
				assert.Equal(t, "sudo was refused and did not run. https://console.example/commands/1", body)
				return nil
			})
			e.Emit(Denied, Fields{Argv0: "sudo", ConsoleURL: "https://console.example/commands/1"})
			assert.Equal(t, "https://console.example/commands/1", <-notified)
			const banner = "==== ALPACON: COMMAND DENIED ====\nsudo was refused and did not run.\nhttps://console.example/commands/1\n================================\n"
			if tc.json {
				assert.Equal(t, "existing stderr\n", stderr.String())
				if tc.tty {
					assert.Equal(t, banner, tty.String())
					assert.True(t, tty.closed, "opened terminal must close")
				} else {
					assert.Empty(t, tty.String())
				}
			} else {
				assert.Equal(t, "existing stderr\n"+banner, stderr.String())
				assert.Empty(t, tty.String())
			}
		})
	}
}

func TestServerValuesAreCleaned(t *testing.T) {
	t.Parallel()
	dirty := "safe\x00\x1b\x7f\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"
	_, body := Format(Denied, Fields{Argv0: "sudo", Server: dirty, Account: dirty, Gate: dirty, Session: dirty}, 0)
	assert.Equal(t, "sudo on safe as safe was refused by the safe gate and did not run. Session safe.", body)
	assert.Equal(t, "safe", clean(dirty))
}

func consoleLinkCases() []struct {
	name, workspace, base, link string
	keep                        bool
} {
	const cloud = "https://my-workspace.ap1.alpacon.io"
	return []struct {
		name, workspace, base, link string
		keep                        bool
	}{
		{"cloud console", cloud, "alpacon.io", "https://alpacon.io/my-workspace/commands/1", true},
		{"same workspace", cloud, "alpacon.io", cloud + "/commands/1", true},
		{"self hosted same origin", "http://10.0.0.5:8080", "0.5", "http://10.0.0.5:8080/commands/1", true},
		{"self hosted different host", "http://10.0.0.5:8080", "", "http://10.0.0.6:8080/commands/1", false},
		{"prefix lookalike", cloud, "alpacon.io", "https://evil-alpacon.io/", false},
		{"suffix lookalike", cloud, "alpacon.io", "https://alpacon.io.evil.com/", false},
		{"base nondefault port", cloud, "alpacon.io", "https://alpacon.io:8443/", false},
		{"different scheme", cloud, "alpacon.io", "http://alpacon.io/", false},
		{"userinfo", cloud, "alpacon.io", "https://alice@alpacon.io/", false},
		{"file", cloud, "alpacon.io", "file:///tmp/approval", false},
		{"javascript", cloud, "alpacon.io", "javascript:alert%281%29", false},
		{"search", cloud, "alpacon.io", "search-ms:query=approval", false},
		{"no base domain", cloud, "", "https://alpacon.io/", false},
		{"no derived parent", cloud, "", "https://other.ap1.alpacon.io/", false},
		{"base subdomain", cloud, "alpacon.io", "https://console.alpacon.io/", true},
		{"case and trailing dot", cloud, "ALPACON.IO.", "https://CONSOLE.ALPACON.IO./", true},
		{"non ASCII workspace", "https://bücher.example", "", "https://xn--bcher-kva.example/", false},
		{"ASCII punycode same host", "https://xn--bcher-kva.example", "", "https://xn--bcher-kva.example/", true},
		{"non ASCII base", cloud, "bücher.example", "https://console.xn--bcher-kva.example/", false},
		{"login derived IP base", "http://10.0.0.5:8080", "0.5", "http://192.168.0.5/x", false},
		{"login derived public suffix", "https://alpacon.corp.example.co.uk", "co.uk", "https://attacker.co.uk/", false},
		{"self hosted DNS same origin", "https://alpacon.corp.example.co.uk", "co.uk", "https://ALPACON.CORP.EXAMPLE.CO.UK.:443/x", true},
		{"cloud base on unrelated workspace", "https://my-server.example", "alpacon.io", "https://alpacon.io/", false},
		{"cloud base on IP workspace", "https://10.0.0.5", "alpacon.io", "https://alpacon.io/", false},
		{"cloud base on apex workspace", "https://alpacon.io", "alpacon.io", "https://console.alpacon.io/", false},
		{"workspace prefix lookalike", "https://evil-alpacon.io", "alpacon.io", "https://alpacon.io/", false},
		{"fullwidth host", cloud, "alpacon.io", "https://ａｌｐａｃｏｎ.io/", false},
		{"ideographic dot host", cloud, "alpacon.io", "https://console。alpacon.io/", false},
		{"mapped IPv6 against IPv4", "http://10.0.0.5:8080", "0.5", "http://[::ffff:10.0.0.5]:8080/x", false},
		{"mapped IPv6 same host", "http://[::ffff:10.0.0.5]:8080", "", "http://[::ffff:10.0.0.5]:8080/x", false},
		{"mapped IPv6 spelling", "http://[::ffff:a00:5]:8080", "", "http://[::ffff:10.0.0.5]:8080/x", false},
		{"IPv4 against mapped IPv6 workspace", "http://[::ffff:10.0.0.5]:8080", "", "http://10.0.0.5:8080/x", false},
		{"IPv6 spelling", "http://[::1]:8080", "", "http://[0:0:0:0:0:0:0:1]:8080/x", true},
		{"same host default port", cloud, "", cloud + ":443/", true},
		{"base explicit default port", cloud, "alpacon.io", "https://alpacon.io:443/", true},
		{"http default port", "http://my-server", "", "http://my-server:80/", true},
		{"same host wrong port", "http://10.0.0.5:8080", "", "http://10.0.0.5/", false},
		{"IPv6 same host", "http://[::1]:8080", "", "http://[::1]:8080/", true},
		{"control", cloud, "alpacon.io", "https://alpacon.io/\x01", false},
		{"too long", cloud, "alpacon.io", "https://alpacon.io/" + strings.Repeat("a", 2048), false},
		{"at length limit", cloud, "alpacon.io", "https://alpacon.io/" + strings.Repeat("a", 2048-len("https://alpacon.io/")), true},
		{"relative", cloud, "alpacon.io", "/commands/1", false},
		{"no host", cloud, "alpacon.io", "https:///commands/1", false},
		{"invalid escape", cloud, "alpacon.io", "https://alpacon.io/%zz", false},
		{"invalid port", cloud, "alpacon.io", "https://alpacon.io:65536/", false},
		{"empty port", cloud, "alpacon.io", "https://alpacon.io:/", false},
		{"missing workspace", "", "alpacon.io", "https://alpacon.io/", false},
	}
}

func TestConsoleURLValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range consoleLinkCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := ""
			if tc.keep {
				want = tc.link
			}
			assert.Equal(t, want, consoleLink(tc.link, tc.workspace, tc.base))
			var output bytes.Buffer
			e := New(&output, nil, nil)
			e.WorkspaceURL, e.BaseDomain = tc.workspace, tc.base
			e.statePath = func() (string, error) { return "", errors.New("no state") }
			e.headless = func() bool { return false }
			e.notifier = notifyFunc(func(ctx context.Context, title, body, link string) error {
				assert.Equal(t, want, link)
				_, wantBody := Format(ApprovalPending, Fields{}, 0)
				if want != "" {
					wantBody += " " + want
				}
				assert.Equal(t, wantBody, body)
				args := platformNotificationArguments(ctx, title, body, link)
				if !tc.keep {
					for _, arg := range args {
						assert.NotContains(t, arg, tc.link)
						assert.NotContains(t, arg, base64.StdEncoding.EncodeToString([]byte(tc.link)))
					}
				}
				return nil
			})
			e.Emit(ApprovalPending, Fields{ConsoleURL: tc.link})
			if !tc.keep {
				assert.NotContains(t, output.String(), tc.link)
			} else {
				assert.Contains(t, output.String(), "\n"+want+"\n")
			}
		})
	}
}
