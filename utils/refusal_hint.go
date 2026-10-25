package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	for e := range errorChain(err) {
		if carrier, ok := e.(hintCarrier); ok {
			if hint := SanitizeServerHint(carrier.ErrorHint()); hint != "" {
				return hint
			}
		}
	}
	return ""
}

// RefusalNextActions is the server's own guidance for a refused request, as
// the follow-ups an error output shows under the error line. The hint is
// printed as sent, never rephrased or matched against a local table, so a new
// refusal code needs no CLI change to be explained. Nil when err carries none.
func RefusalNextActions(err error) []NextAction {
	hint := RefusalHint(err)
	if hint == "" {
		return nil
	}
	return []NextAction{{Description: hint}}
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
