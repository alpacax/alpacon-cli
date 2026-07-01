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

func FormatJSON(value any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
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
		_, _ = fmt.Fprintln(os.Stdout, string(data))
		return
	}

	writer, cleanup := WriteToPager()
	defer cleanup()

	tw := tabwriter.NewWriter(writer, 0, 0, 3, ' ', 0)

	numFields := s.Type().Elem().NumField()
	headers := make([]string, numFields)
	for i := 0; i < numFields; i++ {
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
		for j := 0; j < numFields; j++ {
			row[j] = fmt.Sprintf("%v", s.Index(i).Field(j))
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
		_, _ = fmt.Fprintln(os.Stdout, buf.String())
		return
	}

	var prettyJSON bytes.Buffer
	err := json.Indent(&prettyJSON, body, "", "    ")
	if err != nil {
		CliErrorWithExit("Parsing data: Expected a JSON format.")
	}

	formattedJson := strings.ReplaceAll(prettyJSON.String(), "\\n", "\n")
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
			// Check if this is start of a new word
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
