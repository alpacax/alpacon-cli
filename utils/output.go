package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/olekukonko/tablewriter"
)

func PrintTable(slice interface{}) {
	s := reflect.ValueOf(slice)

	if s.Kind() != reflect.Slice {
		CliErrorWithExit("Parsing data: Expected a list format.")
	}

	writer, cleanup := WriteToPager()
	defer cleanup()

	table := tablewriter.NewWriter(writer)
	table.SetBorder(false)
	table.SetAutoFormatHeaders(true)
	table.SetAutoWrapText(false)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetTablePadding("   ")
	table.SetNoWhiteSpace(true)

	headers := make([]string, s.Type().Elem().NumField())
	for i := 0; i < s.Type().Elem().NumField(); i++ {
		headers[i] = s.Type().Elem().Field(i).Name
	}
	table.SetHeader(headers)

	for i := 0; i < s.Len(); i++ {
		row := make([]string, s.Type().Elem().NumField())
		for j := 0; j < s.Type().Elem().NumField(); j++ {
			value := s.Index(i).Field(j)
			row[j] = fmt.Sprintf("%v", value)
		}
		table.Append(row)
	}

	table.Render()
}

func PrintJson(body []byte) {
	var prettyJSON bytes.Buffer
	err := json.Indent(&prettyJSON, body, "", "    ")
	if err != nil {
		CliErrorWithExit("Parsing data: Expected a json format")
	}

	formattedJson := strings.Replace(prettyJSON.String(), "\\n", "\n", -1)
	formattedJson = strings.Replace(formattedJson, "\\t", "\t", -1)

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
