package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

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
		headers[i] = s.Type().Elem().Field(i).Name
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
	fmt.Println(Blue(header))
}

func PrettyJSON(data []byte) (*bytes.Buffer, error) {
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, data, "", "\t"); err != nil {
		return nil, err
	}

	return &prettyJSON, nil
}
