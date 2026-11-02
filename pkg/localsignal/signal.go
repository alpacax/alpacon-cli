// Package localsignal draws attention to a refusal or an approval that needs a person.
package localsignal

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/alpacax/alpacon-cli/pkg/configdir"
)

const (
	Denied          Kind = "denied"
	ApprovalPending Kind = "approval_pending"
	Paused          Kind = "paused"
	notifyTimeout        = 2 * time.Second
)

type Kind string

type Fields struct {
	Argv0   string
	Server  string
	Account string
	Gate    string
	Session string
}

type Notifier interface {
	Notify(context.Context, string, string) error
}

type platformNotifier struct{}

type Emitter struct {
	notifier  Notifier
	writer    io.Writer
	red       func(string) string
	debug     func(string, ...any)
	headless  func() bool
	statePath func() (string, error)
	now       func() time.Time
}

func New(w io.Writer, red func(string) string, debug func(string, ...any)) *Emitter {
	return &Emitter{
		notifier: platformNotifier{}, writer: w, red: red, debug: debug,
		headless: platformHeadless, now: time.Now,
		statePath: func() (string, error) {
			dir, err := configdir.Dir()
			return filepath.Join(dir, "state", "signals.json"), err
		},
	}
}

func Emit(kind Kind, fields Fields) {
	New(os.Stderr, nil, nil).Emit(kind, fields)
}

// Emit waits only for the bounded notification attempt, including in a process about to exit.
func (e *Emitter) Emit(kind Kind, fields Fields) {
	statePath, err := e.statePath()
	folded, emit := 0, true
	if err == nil {
		folded, emit, err = allow(statePath, fields.Session, kind, e.now)
	}
	if err != nil {
		e.log("Local signal state unavailable: %s", err)
	}
	if !emit {
		return
	}
	title, body := Format(kind, fields, folded)
	if title == "" {
		return
	}
	banner := fmt.Sprintf("==== %s ====\n%s\n================================\n", strings.ToUpper(title), body)
	if e.red != nil {
		banner = e.red(banner)
	}
	_, _ = io.WriteString(e.writer, banner)
	if e.headless() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.notifier.Notify(ctx, title, body) }()
	select {
	case err := <-done:
		if err != nil {
			e.log("Local notification failed: %s", err)
		}
	case <-ctx.Done():
		e.log("Local notification timed out")
	}
}

func (e *Emitter) log(message string, args ...any) {
	if e.debug != nil {
		e.debug(message, args...)
	}
}

// Argv0 takes only the first shell word, recognizing quoted executable paths on either platform.
func Argv0(command string) string {
	command = strings.TrimSpace(command)
	var word strings.Builder
	var quote rune
	for _, r := range command {
		if quote == 0 && unicode.IsSpace(r) {
			break
		}
		if r == '\'' || r == '"' {
			if quote == 0 {
				quote = r
				continue
			}
			if quote == r {
				quote = 0
				continue
			}
		}
		word.WriteRune(r)
	}
	return clean(path.Base(strings.ReplaceAll(word.String(), `\`, "/")))
}

func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s))
}

func Format(kind Kind, f Fields, folded int) (title, body string) {
	command := ""
	if strings.TrimSpace(f.Argv0) != "" {
		command = Argv0(f.Argv0)
	}
	if command == "" || command == "." {
		command = "Command"
	}
	if server := clean(f.Server); server != "" {
		command += " on " + server
	}
	if account := clean(f.Account); account != "" {
		command += " as " + account
	}
	session := clean(f.Session)
	switch kind {
	case Denied:
		title, body = "Alpacon: command denied", command+" was refused"
		if gate := clean(f.Gate); gate != "" {
			body += " by the " + gate + " gate"
		}
		body += " and did not run."
		if session != "" {
			body += " Session " + session + "."
		}
	case ApprovalPending:
		title = "Alpacon: approval pending"
		if strings.TrimSpace(f.Argv0) == "" {
			body = "Work Session"
			if session != "" {
				body += " " + session
			}
			body += " is waiting for approval."
		} else {
			body = command + " is waiting for approval."
			if session != "" {
				body += " Session " + session + "."
			}
		}
		body += " Open the approval or wait for an approver."
	case Paused:
		title, body = "Alpacon: session paused", "Work Session"
		if session != "" {
			body += " " + session
		}
		body += " is paused after a critical denial. The owner or an approver can resume or end it."
	default:
		return "", ""
	}
	if folded > 0 {
		body += fmt.Sprintf(" +%d more.", folded)
	}
	return title, body
}

func headless(platform string, lookupEnv func(string) (string, bool), sessionZero bool) bool {
	ci, _ := lookupEnv("CI")
	_, sshConnection := lookupEnv("SSH_CONNECTION")
	_, sshTTY := lookupEnv("SSH_TTY")
	if ci != "" || sshConnection || sshTTY {
		return true
	}
	if platform == "linux" {
		_, display := lookupEnv("DISPLAY")
		_, wayland := lookupEnv("WAYLAND_DISPLAY")
		_, bus := lookupEnv("DBUS_SESSION_BUS_ADDRESS")
		return (!display && !wayland) || !bus
	}
	return platform == "windows" && sessionZero
}
