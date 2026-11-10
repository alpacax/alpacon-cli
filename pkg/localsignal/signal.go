// Package localsignal draws attention to a refusal or an approval that needs a person.
package localsignal

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alpacax/alpacon-cli/pkg/configdir"
	"golang.org/x/term"
)

const (
	Denied          Kind = "denied"
	ApprovalPending Kind = "approval_pending"
	Paused          Kind = "paused"
	notifyTimeout        = 2 * time.Second
)

// These dependencies are replaceable for callers that embed the emitter.
var NewNotifier = func() Notifier { return platformNotifier{} }
var OpenTTY = openTTY

var executableName = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)
var sessionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var assignmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
var accountName = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}\$?$`)

type Kind string

type ConsoleOrigin struct {
	WorkspaceURL string
	BaseDomain   string
}

type Fields struct {
	Command    bool
	Origin     *ConsoleOrigin
	Argv0      string
	Server     string
	Account    string
	Gate       string
	Session    string
	ConsoleURL string
	// Assignee marks a held command whose confirmation is the session's assignee's
	// own, not an approver's. It changes the wording only, never the kind.
	Assignee bool
}

type Notifier interface {
	Notify(context.Context, string, string, string) error
}

type platformNotifier struct{}

type Emitter struct {
	JSON         bool
	WorkspaceURL string
	BaseDomain   string
	openTTY      func() (io.WriteCloser, error)
	isTerminal   func(int) bool
	notifier     Notifier
	writer       io.Writer
	red          func(string) string
	debug        func(string, ...any)
	headless     func() bool
	statePath    func() (string, error)
	now          func() time.Time
}

func New(w io.Writer, red func(string) string, debug func(string, ...any)) *Emitter {
	return &Emitter{
		notifier: NewNotifier(), openTTY: OpenTTY, writer: w, red: red, debug: debug,
		headless: platformHeadless, now: time.Now, isTerminal: term.IsTerminal,
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
		workspace, _ := url.Parse(e.WorkspaceURL)
		host := ""
		if workspace != nil {
			host = strings.ToLower(workspace.Host)
		}
		folded, emit, err = allow(statePath, host, fields.Session, kind, e.now)
	}
	if err != nil {
		e.log("Local signal state unavailable: %s", err)
	}
	title, body := Format(kind, fields, 0)
	if title == "" {
		return
	}
	link := consoleLink(fields.ConsoleURL, e.WorkspaceURL, e.BaseDomain)
	if utf8.RuneCountInString(body)+1+utf8.RuneCountInString(link) > 256 {
		link = ""
	}
	bannerBody := body
	if link != "" {
		bannerBody += "\n" + link
	}
	banner := fmt.Sprintf("==== %s ====\n%s\n================================\n", strings.ToUpper(title), capText(bannerBody, 256))
	if e.red != nil {
		banner = e.red(banner)
	}
	ttyOutput := e.JSON
	if file, ok := e.writer.(interface{ Fd() uintptr }); ok {
		ttyOutput = ttyOutput || e.isTerminal(int(file.Fd()))
	}
	if ttyOutput {
		if err := writeTTY(e.openTTY, banner); err != nil && !e.JSON {
			_, _ = io.WriteString(e.writer, banner)
		}
	} else {
		_, _ = io.WriteString(e.writer, banner)
	}
	if !emit || e.headless() {
		return
	}
	_, body = Format(kind, fields, folded)
	// The fold suffix lengthens the body, so the link must fit again or be left out.
	notifyLink := link
	if utf8.RuneCountInString(body)+1+utf8.RuneCountInString(notifyLink) > 256 {
		notifyLink = ""
	}
	if notifyLink != "" {
		body += " " + notifyLink
	}
	body = capText(body, 256)
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.notifier.Notify(ctx, title, body, notifyLink) }()
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

// Argv0 discards assignments and shell syntax before accepting a plain executable name.
func Argv0(command string) string {
	if len(command) > 4096 {
		return ""
	}
	command = strings.TrimLeft(command, " \t\n\r")
	for {
		prefix := assignmentName.FindString(command)
		var quote rune
		escaped := false
		end := len(command)
		for i, r := range command[len(prefix):] {
			if prefix == "" && strings.ContainsRune("'\"\\", r) {
				return ""
			}
			if r > unicode.MaxASCII || (unicode.IsControl(r) && !asciiSpace(r)) {
				return ""
			}
			if r == '`' || strings.HasPrefix(command[len(prefix)+i:], "$(") || strings.HasPrefix(command[len(prefix)+i:], "${") {
				return ""
			}
			if prefix == "" && strings.ContainsRune(";|&<>()", r) {
				break
			}
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' && quote != '\'' {
				escaped = true
				continue
			}
			if quote != 0 {
				switch r {
				case quote:
					quote = 0
				case '\'', '"':
					return ""
				}
				continue
			}
			if r == '\'' || r == '"' {
				quote = r
				continue
			}
			if asciiSpace(r) {
				end = len(prefix) + i
				break
			}
			if prefix != "" && strings.ContainsRune(";|&<>()$`", r) {
				return ""
			}
		}
		if quote != 0 {
			return ""
		}
		if prefix == "" {
			break
		}
		command = strings.TrimLeft(command[end:], " \t\n\r")
	}
	end := strings.IndexFunc(command, func(r rune) bool {
		return asciiSpace(r) || strings.ContainsRune(";|&<>()$`'\"\\", r)
	})
	if end >= 0 {
		command = command[:end]
	}
	if command == "" {
		return ""
	}
	name := path.Base(command)
	if !executableName.MatchString(name) {
		return ""
	}
	return name
}

func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
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
	if server := displayName(f.Server); server != "" {
		command += " on " + server
	}
	if accountName.MatchString(f.Account) {
		command += " as " + f.Account
	}
	session := f.Session
	if !sessionName.MatchString(session) {
		session = ""
	}
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
		isCommand := f.Command || strings.TrimSpace(f.Argv0) != ""
		switch {
		case !isCommand:
			body = "Work Session"
			if session != "" {
				body += " " + session
			}
			body += " is waiting for approval. Open the approval or wait for an approver."
		case f.Assignee:
			// Only a held command can be the session assignee's own to confirm.
			title = "Alpacon: confirmation pending"
			body = command + " is waiting for your own confirmation, not an approver's."
			if session != "" {
				body += " Session " + session + "."
			}
			body += " Confirm or reject it in the Alpacon console."
		default:
			body = command + " is waiting for approval."
			if session != "" {
				body += " Session " + session + "."
			}
			body += " Open the approval or wait for an approver."
		}
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
	return title, capText(body, 256)
}

func asciiSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func capText(s string, limit int) string {
	for i := range s {
		if limit == 0 {
			return s[:i]
		}
		limit--
	}
	return s
}

func displayName(s string) string {
	s = clean(s)
	if strings.Contains(s, "://") {
		return ""
	}
	return capText(s, 64)
}

func writeTTYNow(open func() (io.WriteCloser, error), banner string) error {
	tty, err := open()
	if err != nil {
		return err
	}
	defer func() { _ = tty.Close() }()
	n, err := io.WriteString(tty, banner)
	if err == nil && n != len(banner) {
		return io.ErrShortWrite
	}
	return err
}
