package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"unicode"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
)

func PrintTable(slice any) {
	s := reflect.ValueOf(slice)

	if s.Kind() != reflect.Slice {
		CliErrorWithExit("Parsing data: Expected a list format.")
	}

	writer, cleanup := WriteToPager()
	defer cleanup()

	table := tablewriter.NewTable(writer,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders: tw.BorderNone,
			Settings: tw.Settings{
				Separators: tw.Separators{
					BetweenRows:    tw.Off,
					BetweenColumns: tw.Off,
				},
				Lines: tw.Lines{
					ShowHeaderLine: tw.Off,
				},
			},
		})),
		tablewriter.WithConfig(tablewriter.Config{
			Header: tw.CellConfig{
				Formatting: tw.CellFormatting{AutoFormat: tw.On},
				Alignment:  tw.CellAlignment{Global: tw.AlignLeft},
				Padding:    tw.CellPadding{Global: tw.Padding{Left: "", Right: "   "}},
			},
			Row: tw.CellConfig{
				Formatting: tw.CellFormatting{AutoWrap: tw.WrapNone},
				Alignment:  tw.CellAlignment{Global: tw.AlignLeft},
				Padding:    tw.CellPadding{Global: tw.Padding{Left: "", Right: "   "}},
			},
			Behavior: tw.Behavior{TrimSpace: tw.On},
		}),
	)

	headers := make([]string, s.Type().Elem().NumField())
	for i := 0; i < s.Type().Elem().NumField(); i++ {
		field := s.Type().Elem().Field(i)
		if tag := field.Tag.Get("table"); tag != "" {
			headers[i] = tag
		} else {
			headers[i] = camelToWords(field.Name)
		}
	}
	table.Header(headers)

	for i := 0; i < s.Len(); i++ {
		row := make([]string, s.Type().Elem().NumField())
		for j := 0; j < s.Type().Elem().NumField(); j++ {
			value := s.Index(i).Field(j)
			row[j] = fmt.Sprintf("%v", value)
		}
		_ = table.Append(row)
	}

	_ = table.Render()
}

func PrintJson(body []byte) {
	var prettyJSON bytes.Buffer
	err := json.Indent(&prettyJSON, body, "", "    ")
	if err != nil {
		CliErrorWithExit("Parsing data: Expected a json format")
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
