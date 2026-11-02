package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxServerHintBytes is the most a server hint may take: the server keeps its
// own hints within it, and the CLI cuts anything longer, since nothing between
// the server and this process is trusted to have kept it.
const MaxServerHintBytes = 256

// hintCarrier is implemented by an error that carries the "hint" a coded
// 402/403/405/429 refusal sent beside "code": one sentence or two from the
// server saying what the caller can do next.
type hintCarrier interface {
	ErrorHint() string
}

// SanitizeServerHint makes a server hint safe to print on one terminal line:
// escape sequences, format characters and control characters (newlines
// included) are removed, surrounding space is trimmed, and the result is cut
// to MaxServerHintBytes without splitting a character.
func SanitizeServerHint(hint string) string {
	clean := strings.TrimSpace(SanitizeTerminalText(strings.ToValidUTF8(hint, "")))
	if len(clean) <= MaxServerHintBytes {
		return clean
	}
	cut := MaxServerHintBytes
	for cut > 0 && !utf8.RuneStart(clean[cut]) {
		cut--
	}
	return strings.TrimSpace(clean[:cut])
}

// IsRefusalStatus reports whether statusCode is one the server answers a
// coded refusal with, the only responses whose "hint" is the server's
// guidance rather than a request field of that name.
func IsRefusalStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusPaymentRequired, http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusTooManyRequests:
		return true
	}
	return false
}

// ParseRefusalHint returns the "hint" string a 402/403/405/429 body carries,
// sanitized (SanitizeServerHint), or "" when there is none or nothing
// printable is left of it. A server that predates the field sends none. A
// request that does not go through the shared client reads its refusal's
// hint here, so its error can carry one too.
func ParseRefusalHint(body []byte, statusCode int) string {
	if !IsRefusalStatus(statusCode) {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	value, _ := parsed["hint"].(string)
	return SanitizeServerHint(value)
}

// RefusalHint returns the sanitized hint the first error in err's chain that
// carries one holds, or "" when none does.
func RefusalHint(err error) string {
	if hints := refusalHints(err); len(hints) > 0 {
		return hints[0]
	}
	return ""
}

// refusalHints returns every distinct sanitized hint in err's chain, in chain
// order. An error that joins several refusals, such as a batch in which more
// than one item was refused, can carry more than one; the same hint on several
// of them counts once.
func refusalHints(err error) []string {
	var hints []string
	seen := map[string]bool{}
	for e := range errorChain(err) {
		carrier, ok := e.(hintCarrier)
		if !ok {
			continue
		}
		if hint := SanitizeServerHint(carrier.ErrorHint()); hint != "" && !seen[hint] {
			seen[hint] = true
			hints = append(hints, hint)
		}
	}
	return hints
}

const refusalNextDescription = "Where the server says to check on this refusal"

// GatePlan is the "gate" of a plan refusal, whose own message already states
// when its limit resets, so no Retry-After action is added for it.
const GatePlan = "plan"

// nextCarrier is implemented by an error that carries a refusal's "next" path.
type nextCarrier interface {
	ErrorNext() string
}

// tokenNextCarrier is implemented by an error whose request went out under an
// API token, which the path "next" names may refuse.
type tokenNextCarrier interface {
	ErrorNextUnderAPIToken() bool
}

// selfRelativePath matches a path whose every segment is plain characters that
// no shell, terminal or URL parser reads differently, with no "." or ".." segment.
var selfRelativePath = regexp.MustCompile(`^(/[A-Za-z0-9_~-][A-Za-z0-9_.~-]*)+/?$`)

// ParseRefusalNext returns the "next" string a 402/403/405/429 body carries,
// or "" on any other status, where a field of that name is a request field.
func ParseRefusalNext(body []byte, statusCode int) string {
	if !IsRefusalStatus(statusCode) {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	next, _ := parsed["next"].(string)
	return next
}

// refusalNext returns the first "next" in err's chain and whether its request
// used an API token, or "" when there is none or it is not a path safe to hand
// to `alpacon api` as one shell word.
func refusalNext(err error) (next string, underAPIToken bool) {
	for e := range errorChain(err) {
		carrier, ok := e.(nextCarrier)
		if !ok {
			continue
		}
		next := carrier.ErrorNext()
		if next == "" {
			continue
		}
		if len(next) > MaxServerHintBytes || !selfRelativePath.MatchString(next) {
			return "", false
		}
		tokenCarrier, ok := e.(tokenNextCarrier)
		return next, ok && tokenCarrier.ErrorNextUnderAPIToken()
	}
	return "", false
}

// RefusalNextActions is the server's guidance for a refused request, shown
// under the error line: each distinct hint as sent, then where to read the
// request's state ("next"), then the Retry-After wait of a gated refusal other
// than a plan limit. Nil when err carries none. Under an API token "next" is
// text only, since a path the server names may refuse token authentication.
func RefusalNextActions(err error) []NextAction {
	var actions []NextAction
	for _, hint := range refusalHints(err) {
		actions = append(actions, NextAction{Description: hint})
	}
	switch next, underAPIToken := refusalNext(err); {
	case next == "":
	case underAPIToken:
		actions = append(actions, NextAction{Description: refusalNextDescription + ", which an API token cannot read: " + next})
	default:
		actions = append(actions, NextAction{Command: "alpacon api " + next, Description: refusalNextDescription})
	}
	gate, _ := ParseErrorGateAndMissing(err)
	if wait := RetryAfter(err); wait > 0 && gate != "" && gate != GatePlan {
		actions = append(actions, NextAction{Description: fmt.Sprintf("Retry after %ds", retryAfterSeconds(wait))})
	}
	return actions
}

// Reword returns an error that reads as message and keeps err in its chain,
// for a call site that words a failure itself: the Cli* error helpers still
// find err's server hint and print it under message, and err's code still
// reads through ParseErrorResponse.
func Reword(err error, message string) error {
	return &rewordedError{message: message, err: err}
}

type rewordedError struct {
	message string
	err     error
}

func (e *rewordedError) Error() string { return e.message }
func (e *rewordedError) Unwrap() error { return e.err }

// PrintRefusalHints writes err's refusal guidance to w, one "Hint:" line per
// RefusalNextActions entry, for output written outside the Cli* helpers.
func PrintRefusalHints(w io.Writer, err error) {
	printRefusalNextActions(w, err)
}

// HintOnly returns an error that reads as message and carries refusal's hint,
// sanitized, and nothing else of it: no code, no status, no wrapped error. A
// timeout uses it to keep the guidance of the refusal its last poll got while
// staying a timeout to everything that classifies errors, such as the
// work-session gate or an MFA retry. Without a hint on refusal it is a plain
// error.
func HintOnly(message string, refusal error) error {
	return &hintOnlyError{message: message, hint: RefusalHint(refusal)}
}

type hintOnlyError struct {
	message, hint string
}

func (e *hintOnlyError) Error() string     { return e.message }
func (e *hintOnlyError) ErrorHint() string { return e.hint }

// RawTerminalHintLines renders err's refusal guidance for a terminal that may
// be in raw mode, where a bare newline leaves the cursor where it stood: one
// "Hint:" line per RefusalNextActions entry, each ending in \r\n.
func RawTerminalHintLines(err error) string {
	var sb strings.Builder
	for _, action := range RefusalNextActions(err) {
		fmt.Fprintf(&sb, "%s %s\r\n", Yellow("Hint:"), action.PlainText())
	}
	return sb.String()
}

// refusalErrorIn returns the first argument that is an error carrying refusal
// guidance, so the Cli* error helpers can print it under the line they render
// that error into.
func refusalErrorIn(args []any) error {
	for _, arg := range args {
		if err, ok := arg.(error); ok && len(RefusalNextActions(err)) > 0 {
			return err
		}
	}
	return nil
}

// printRefusalNextActions writes err's refusal guidance, one "Hint:" line per
// action. Nothing is written for an error that carries none.
func printRefusalNextActions(w io.Writer, err error) {
	for _, action := range RefusalNextActions(err) {
		_, _ = fmt.Fprintf(w, "%s %s\n", Yellow("Hint:"), action.PlainText())
	}
}
