package utils

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type outputTestItem struct {
	Name string `table:"Name" json:"name"`
	ID   int    `table:"ID"   json:"id"`
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func withFormat(format string, fn func()) {
	old := OutputFormat
	defer func() { OutputFormat = old }()
	OutputFormat = format
	fn()
}

func TestPrintTable_JSONOutput(t *testing.T) {
	items := []outputTestItem{
		{Name: "alpha", ID: 1},
		{Name: "beta", ID: 2},
	}
	var got string
	withFormat("json", func() {
		got = captureStdout(t, func() { PrintTable(items) })
	})
	assert.JSONEq(t, `[{"name":"alpha","id":1},{"name":"beta","id":2}]`, strings.TrimSpace(got))
}

func TestPrintTable_JSONOutput_EmptySlice(t *testing.T) {
	items := []outputTestItem{}
	var got string
	withFormat("json", func() {
		got = captureStdout(t, func() { PrintTable(items) })
	})
	assert.Equal(t, "[]\n", got)
}

func TestPrintTable_JSONOutput_NilSlice(t *testing.T) {
	var items []outputTestItem
	var got string
	withFormat("json", func() {
		got = captureStdout(t, func() { PrintTable(items) })
	})
	assert.Equal(t, "[]\n", got)
}

func TestPrintTable_TableOutput(t *testing.T) {
	items := []outputTestItem{{Name: "alpha", ID: 1}}
	var got string
	withFormat("table", func() {
		got = captureStdout(t, func() { PrintTable(items) })
	})
	assert.Contains(t, got, "NAME")
	assert.Contains(t, got, "ID")
	assert.Contains(t, got, "alpha")
	assert.Contains(t, got, "1")
}

func TestPrintJson_JSONOutput(t *testing.T) {
	pretty := []byte(`{
  "name": "alpha",
  "id": 1
}`)
	var got string
	withFormat("json", func() {
		got = captureStdout(t, func() { PrintJson(pretty) })
	})
	assert.Equal(t, `{"name":"alpha","id":1}`, strings.TrimSpace(got))
	assert.NotContains(t, got, "\n  ")
}

func TestPrintJson_TableOutput(t *testing.T) {
	compact := []byte(`{"name":"alpha","id":1}`)
	var got string
	withFormat("table", func() {
		got = captureStdout(t, func() { PrintJson(compact) })
	})
	assert.Contains(t, got, "\"name\": \"alpha\"")
	assert.Contains(t, got, "\"id\": 1")
}
