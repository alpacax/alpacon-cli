package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Valid values for the --output persistent flag.
const (
	OutputFormatTable = "table"
	OutputFormatJSON  = "json"
)

// OutputFormat holds the value of the --output persistent flag.
// Bound by cmd/root.go; read by PrintTable and PrintJson.
var OutputFormat string

type JSONErrorEnvelope[T any] struct {
	OK          bool         `json:"ok"`
	ExitCode    int          `json:"exit_code,omitempty"`
	ErrorCode   string       `json:"error_code,omitempty"`
	Message     string       `json:"message"`
	Reason      string       `json:"reason,omitempty"`
	Context     T            `json:"context"`
	NextActions []NextAction `json:"next_actions,omitempty"`
}

// NextAction is one actionable follow-up. Command is a pure, runnable command
// (no inline comments) for machine consumers; Description carries the human hint.
// Either may be empty—a pure command needs no hint, and a guidance-only pointer
// (e.g. "approve it in the console") carries no runnable command—so both fields
// are omitempty.
type NextAction struct {
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
}

// PlainText renders the action as a human-facing line: "command  # description",
// or just the command or description when the other is empty.
func (a NextAction) PlainText() string {
	switch {
	case a.Command != "" && a.Description != "":
		return a.Command + "  # " + a.Description
	case a.Command != "":
		return a.Command
	default:
		return a.Description
	}
}

// escapeJSONControls rewrites DEL, the C1 block, and Unicode format characters as
// \u escapes. encoding/json escapes only below U+0020, so these reach the terminal
// untouched: 8-bit CSI opens a control sequence there and a bidi override reorders
// the line. Escaping rather than stripping keeps the value a decoder reads back
// unchanged. Valid JSON carries them inside strings alone, so scanning needs no
// parser. json.Indent does not validate UTF-8, so C1 can arrive as a bare byte;
// decoding by rune keeps the continuation bytes of valid runes from being mistaken
// for one.
func escapeJSONControls(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		switch {
		case r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r):
			out = appendUnicodeEscape(out, r)
		case r == utf8.RuneError && size == 1 && b[i] >= 0x80 && b[i] <= 0x9f:
			out = appendUnicodeEscape(out, rune(b[i]))
		default:
			out = append(out, b[i:i+size]...)
		}
		i += size
	}
	return out
}

// appendUnicodeEscape writes r as JSON \u escapes, which are UTF-16 code units:
// anything past the BMP needs a surrogate pair.
func appendUnicodeEscape(out []byte, r rune) []byte {
	if r > 0xffff {
		hi, lo := utf16.EncodeRune(r)
		return append(out, fmt.Sprintf(`\u%04x\u%04x`, hi, lo)...)
	}
	return append(out, fmt.Sprintf(`\u%04x`, r)...)
}

func FormatJSON(value any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimRight(string(escapeJSONControls(buf.Bytes())), "\n"), nil
}

func PrintJSONValue(w io.Writer, value any) error {
	rendered, err := FormatJSON(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, rendered)
	return err
}

func PrintJSONError[T any](w io.Writer, envelope JSONErrorEnvelope[T]) {
	envelope.OK = false
	if err := PrintJSONValue(w, envelope); err != nil {
		_, _ = fmt.Fprintf(w, `{"ok":false,"error_code":%q}`+"\n", envelope.ErrorCode)
	}
}

func PrintTable(slice any) {
	s := reflect.ValueOf(slice)

	if s.Kind() != reflect.Slice {
		CliErrorWithExit("Parsing data: Expected a list format.")
	}

	if OutputFormat == OutputFormatJSON {
		if s.IsNil() || s.Len() == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "[]")
			return
		}
		data, err := json.MarshalIndent(slice, "", "  ")
		if err != nil {
			CliErrorWithExit("Failed to marshal data to JSON: %s", err)
		}
		_, _ = fmt.Fprintln(os.Stdout, string(escapeJSONControls(data)))
		return
	}

	writer, cleanup := WriteToPager()
	defer cleanup()

	tw := tabwriter.NewWriter(writer, 0, 0, 3, ' ', 0)

	numFields := s.Type().Elem().NumField()
	headers := make([]string, numFields)
	for i := range numFields {
		field := s.Type().Elem().Field(i)
		if tag := field.Tag.Get("table"); tag != "" {
			headers[i] = strings.ToUpper(tag)
		} else {
			headers[i] = strings.ToUpper(camelToWords(field.Name))
		}
	}
	_, _ = fmt.Fprintln(tw, strings.Join(headers, "\t"))

	for i := 0; i < s.Len(); i++ {
		row := make([]string, numFields)
		for j := range numFields {
			// API values are untrusted: a control sequence here rewrites the reader's terminal.
			// Newlines drop instead of becoming spaces as in cmd/event/render.go: a cell must
			// not spill into a second row. --output json returns the value whole.
			row[j] = SanitizeTerminalText(fmt.Sprintf("%v", s.Index(i).Field(j)))
		}
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}

	_ = tw.Flush()
}

func PrintJson(body []byte) {
	if OutputFormat == OutputFormatJSON {
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err != nil {
			CliErrorWithExit("Parsing data: Expected a JSON format.")
		}
		_, _ = fmt.Fprintln(os.Stdout, string(escapeJSONControls(buf.Bytes())))
		return
	}

	var prettyJSON bytes.Buffer
	err := json.Indent(&prettyJSON, body, "", "    ")
	if err != nil {
		CliErrorWithExit("Parsing data: Expected a JSON format.")
	}

	formattedJson := strings.ReplaceAll(string(escapeJSONControls(prettyJSON.Bytes())), "\\n", "\n")
	formattedJson = strings.ReplaceAll(formattedJson, "\\t", "\t")

	fmt.Println(formattedJson)
}

func PrintHeader(header string) {
	fmt.Fprintln(os.Stderr, Blue(header))
}

// camelToWords converts PascalCase/camelCase to space-separated words.
// e.g., "RequestedAt" → "Requested At", "IsLdapUser" → "Is Ldap User", "GID" → "GID"
func camelToWords(s string) string {
	if s == "" {
		return s
	}

	var words []string
	start := 0
	runes := []rune(s)

	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) {
			if unicode.IsLower(runes[i-1]) {
				// "requestedAt" → split before "A"
				words = append(words, string(runes[start:i]))
				start = i
			} else if i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				// "HTMLParser" → split "HTM" and "L..."
				words = append(words, string(runes[start:i]))
				start = i
			}
		}
	}
	words = append(words, string(runes[start:]))

	return strings.Join(words, " ")
}

func PrettyJSON(data []byte) (*bytes.Buffer, error) {
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, data, "", "\t"); err != nil {
		return nil, err
	}

	return &prettyJSON, nil
}
