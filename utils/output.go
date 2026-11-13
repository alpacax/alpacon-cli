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
var OutputFormat string

// SetOutputFormat applies an --output value, refusing anything but table or json as a usage error.
func SetOutputFormat(value string) error {
	if value != OutputFormatTable && value != OutputFormatJSON {
		return MarkUsageError(fmt.Errorf("invalid --output value %q (expected %q or %q)", value, OutputFormatTable, OutputFormatJSON))
	}
	OutputFormat = value
	return nil
}

// OutputFormatIn is the last --output value before any "--" in args, for a command line
// whose parse stopped before reaching it.
func OutputFormatIn(args []string) string {
	var format string
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if value, ok := strings.CutPrefix(arg, "--output="); ok {
			format = value
		} else if arg == "--output" && i+1 < len(args) {
			format = args[i+1]
		}
	}
	return format
}

type PartialInfo struct {
	Count int `json:"count"`
}

// JSONErrorEnvelope is the --output json failure line. Fields are additive only; exit_code
// and error_code are always present.
type JSONErrorEnvelope[T any] struct {
	OK             bool           `json:"ok"`
	ExitCode       int            `json:"exit_code"`
	ErrorCode      string         `json:"error_code"`
	Message        string         `json:"message"`
	Command        string         `json:"command,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	Context        T              `json:"context"`
	HTTPStatus     int            `json:"http_status,omitempty"`
	RetryAfter     int            `json:"retry_after,omitempty"`
	Server         map[string]any `json:"server,omitempty"`
	RemoteExitCode *int           `json:"remote_exit_code,omitempty"`
	Partial        *PartialInfo   `json:"partial,omitempty"`
	NextActions    []NextAction   `json:"next_actions,omitempty"`
}

// NextAction is one actionable follow-up: Command is a runnable command with no inline comment,
// for machine consumers, and Description the human hint. Either may be empty, as a guidance-only
// pointer carries no command.
type NextAction struct {
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
}

// PlainText renders the action as a human-facing line: "command  # description",
// or just the command or description when the other is empty. Sanitized here
// because its callers write to the terminal outside the Cli* helpers and call
// sites interpolate server-returned ids into Command; the JSON envelope marshals
// the fields themselves and escapes via escapeJSONControls instead.
func (a NextAction) PlainText() string {
	command := SanitizeTerminalText(a.Command)
	description := SanitizeTerminalText(a.Description)
	switch {
	case command != "" && description != "":
		return command + "  # " + description
	case command != "":
		return command
	default:
		return description
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
		case IsC1OrDEL(r) || unicode.Is(unicode.Cf, r):
			out = appendUnicodeEscape(out, r)
		case r == utf8.RuneError && size == 1 && IsC1OrDEL(rune(b[i])):
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
	out, err := encodeJSON(value, "  ")
	return string(out), err
}

func encodeJSON(value any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return escapeJSONControls(bytes.TrimRight(buf.Bytes(), "\n")), nil
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
	if envelope.Command == "" {
		envelope.Command = CommandName
	}
	if envelope.ErrorCode == "" {
		envelope.ErrorCode = exitCodeFallback(envelope.ExitCode)
	}
	line, err := encodeJSON(envelope, "")
	if err != nil {
		line, _ = encodeJSON(JSONErrorEnvelope[struct{}]{
			ExitCode: envelope.ExitCode, ErrorCode: envelope.ErrorCode, Message: envelope.Message, Command: envelope.Command,
		}, "")
	}
	line = append(line, '\n')
	if w == os.Stderr {
		if stderrMidLine.Swap(false) || activeSpinners.Load() > 0 {
			line = append([]byte{'\n'}, line...)
		}
	}
	_, _ = w.Write(line)
}

// PrintTable prints a slice of row structs as a table, or as JSON under --output json. It returns the
// exit-1 failure when slice is not a slice or does not marshal.
func PrintTable(slice any) error {
	s := reflect.ValueOf(slice)

	if s.Kind() != reflect.Slice {
		return NewExitError("Parsing data: Expected a list format.")
	}

	if OutputFormat == OutputFormatJSON {
		if s.IsNil() || s.Len() == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "[]")
			return nil
		}
		data, err := json.MarshalIndent(slice, "", "  ")
		if err != nil {
			return NewExitError("Failed to marshal data to JSON: %s", err)
		}
		_, _ = fmt.Fprintln(os.Stdout, string(escapeJSONControls(data)))
		return nil
	}

	writer, cleanup := WriteToPager()
	defer cleanup()

	tw := tabwriter.NewWriter(writer, 0, 0, 3, ' ', 0)

	var fields []int
	var headers []string
	for i := range s.Type().Elem().NumField() {
		field := s.Type().Elem().Field(i)
		if field.Tag.Get("table") == "-" {
			continue
		}
		fields = append(fields, i)
		if tag := field.Tag.Get("table"); tag != "" {
			headers = append(headers, strings.ToUpper(tag))
		} else {
			headers = append(headers, strings.ToUpper(camelToWords(field.Name)))
		}
	}
	_, _ = fmt.Fprintln(tw, strings.Join(headers, "\t"))

	for i := range s.Len() {
		row := make([]string, len(fields))
		for j, field := range fields {
			// API values are untrusted, so control sequences are stripped; newlines drop too, not
			// becoming spaces as in cmd/event/render.go, so a cell cannot spill into a second row.
			// --output json returns the value whole.
			row[j] = SanitizeTerminalText(fmt.Sprintf("%v", s.Index(i).Field(field)))
		}
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}

	_ = tw.Flush()
	return nil
}

// PartialTableExitError prints rows to stdout and the reason to stderr, and returns a silent
// ExitCodeGeneralError, since the warning is the failure line. A walk that read nothing is an ordinary failure.
func PartialTableExitError(rows any, msg string, args ...any) error {
	CliWarning(msg, args...)
	if err := PrintTable(rows); err != nil {
		return err
	}
	return NewSilentExitError(ExitCodeGeneralError)
}

// PrintList prints a list a paginated read returned with err. On a nil err it is PrintTable; otherwise
// it prints the rows as partial through PartialTableExitError, or fails with failMsg when there are none.
func PrintList[T any](rows []T, err error, failMsg string) error {
	switch {
	case err != nil && len(rows) > 0:
		return PartialTableExitError(rows, "Showing only the %d entries read before the request failed: %s.", len(rows), err)
	case err != nil:
		return NewExitError("%s: %s.", failMsg, err)
	}
	return PrintTable(rows)
}

// PrintJson prints body to stdout as indented JSON, or returns the exit-1 failure when body is not JSON.
func PrintJson(body []byte) error {
	if OutputFormat == OutputFormatJSON {
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err != nil {
			return notJSONError()
		}
		_, _ = fmt.Fprintln(os.Stdout, string(escapeJSONControls(buf.Bytes())))
		return nil
	}

	var prettyJSON bytes.Buffer
	err := json.Indent(&prettyJSON, body, "", "    ")
	if err != nil {
		return notJSONError()
	}

	formattedJson := strings.ReplaceAll(string(escapeJSONControls(prettyJSON.Bytes())), "\\n", "\n")
	formattedJson = strings.ReplaceAll(formattedJson, "\\t", "\t")

	fmt.Println(formattedJson)
	return nil
}

func notJSONError() error {
	return NewExitError("Parsing data: Expected a JSON format.")
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
