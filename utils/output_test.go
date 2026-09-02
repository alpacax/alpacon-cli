package utils

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
)

type outputTestItem struct {
	Name string `table:"Name" json:"name"`
	ID   int    `table:"ID"   json:"id"`
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
		got = testutil.CaptureStdout(t, func() { PrintTable(items) })
	})
	assert.JSONEq(t, `[{"name":"alpha","id":1},{"name":"beta","id":2}]`, strings.TrimSpace(got))
	assert.Contains(t, got, "\n  ")
}

func TestPrintTable_JSONOutput_EmptySlice(t *testing.T) {
	items := []outputTestItem{}
	var got string
	withFormat("json", func() {
		got = testutil.CaptureStdout(t, func() { PrintTable(items) })
	})
	assert.Equal(t, "[]\n", got)
}

func TestPrintTable_JSONOutput_NilSlice(t *testing.T) {
	var items []outputTestItem
	var got string
	withFormat("json", func() {
		got = testutil.CaptureStdout(t, func() { PrintTable(items) })
	})
	assert.Equal(t, "[]\n", got)
}

func TestPrintTable_JSONOutput_KeepsControlSequencesEscaped(t *testing.T) {
	// Escaped, not stripped: no control byte reaches the terminal and a decoder
	// still reads back what the API sent.
	tests := []struct {
		name    string
		payload string
		escape  string
	}{
		{"7-bit CSI", "a\x1b[2Kb", `\u001b`},
		{"8-bit CSI", "a\u009b2Kb", `\u009b`},
		{"DEL", "a\x7fb", `\u007f`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := []outputTestItem{{Name: tt.payload, ID: 1}}
			var got string
			withFormat("json", func() {
				got = testutil.CaptureStdout(t, func() { PrintTable(items) })
			})
			assert.Contains(t, got, tt.escape)
			assert.NotContains(t, got, "\x1b")
			assert.NotContains(t, got, "\u009b")
			assert.NotContains(t, got, "\x7f")

			var decoded []outputTestItem
			assert.NoError(t, json.Unmarshal([]byte(got), &decoded))
			assert.Equal(t, items, decoded)
		})
	}
}

func TestPrintTable_TableOutput(t *testing.T) {
	items := []outputTestItem{{Name: "alpha", ID: 1}}
	var got string
	withFormat("table", func() {
		got = testutil.CaptureStdout(t, func() { PrintTable(items) })
	})
	assert.Contains(t, got, "NAME")
	assert.Contains(t, got, "ID")
	assert.Contains(t, got, "alpha")
	assert.Contains(t, got, "1")
}

func TestPrintTable_TableOutput_StripsControlSequences(t *testing.T) {
	// Each payload hides the second command behind a control sequence.
	// cell pins the join: the separator drops, it does not become a space.
	tests := []struct {
		name    string
		payload string
		cell    string
	}{
		{"7-bit CSI and CR", "reboot db-01\x1b[2K\rrm -rf /var/lib/postgresql", "reboot db-01rm -rf /var/lib/postgresql"},
		{"8-bit CSI", "reboot db-01\u009b2Krm -rf /var/lib/postgresql", "reboot db-012Krm -rf /var/lib/postgresql"}, // ansiEscapeRE matches ESC only; rests on StripControlChars
		{"bare LF", "reboot db-01\nrm -rf /var/lib/postgresql", "reboot db-01rm -rf /var/lib/postgresql"},
		{"DEL", "reboot db-01\x7frm -rf /var/lib/postgresql", "reboot db-01rm -rf /var/lib/postgresql"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			withFormat("table", func() {
				got = testutil.CaptureStdout(t, func() { PrintTable([]outputTestItem{{Name: tt.payload, ID: 1}}) })
			})
			assert.NotContains(t, got, "\x1b")
			assert.NotContains(t, got, "\r")
			assert.NotContains(t, got, "\u009b")
			assert.NotContains(t, got, "\x7f")
			assert.Contains(t, got, tt.cell)
			lines := strings.Split(strings.TrimSpace(got), "\n")
			assert.Len(t, lines, 2, "header plus a single row")
		})
	}
}

func TestPrintTable_TableOutput_StripsFormatChars(t *testing.T) {
	// Every table command reaches the terminal through here, and a bidi override
	// rewrites the cell without carrying a control byte for the other passes to find.
	var got string
	withFormat("table", func() {
		got = testutil.CaptureStdout(t, func() {
			PrintTable([]outputTestItem{{Name: "denied\u202e\u2066 approved", ID: 1}})
		})
	})
	assert.NotContains(t, got, "\u202e")
	assert.NotContains(t, got, "\u2066")
	assert.Contains(t, got, "denied approved")
}

func TestPrintJson_JSONOutput(t *testing.T) {
	compact := []byte(`{"name":"alpha","id":1}`)
	var got string
	withFormat("json", func() {
		got = testutil.CaptureStdout(t, func() { PrintJson(compact) })
	})
	assert.JSONEq(t, `{"name":"alpha","id":1}`, got)
	assert.Contains(t, got, "\n  \"name\": \"alpha\"")
	assert.Contains(t, got, "\n  \"id\": 1")
}

func TestPrintJson_JSONOutput_EscapesRawControlBytes(t *testing.T) {
	// A server may send these unescaped: JSON only requires escaping below U+0020.
	body := []byte("{\"name\":\"a\u009b2K\x7fb\"}")
	var got string
	withFormat("json", func() {
		got = testutil.CaptureStdout(t, func() { PrintJson(body) })
	})
	assert.Contains(t, got, `\u009b`)
	assert.Contains(t, got, `\u007f`)
	assert.NotContains(t, got, "\u009b")
	assert.NotContains(t, got, "\x7f")

	var decoded map[string]string
	assert.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "a\u009b2K\x7fb", decoded["name"])
}

func TestPrintJson_JSONOutput_EscapesBareC1Byte(t *testing.T) {
	// json.Indent does not validate UTF-8, so a body may carry C1 as a bare byte.
	// Multibyte runes must survive it: their continuation bytes sit in the same range.
	tests := []struct {
		name string
		body string
		want string
	}{
		{"bare C1", "{\"name\":\"a\x9b2Kb\"}", "a\u009b2Kb"},
		{"multibyte rune", `{"name":"가나"}`, "가나"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			withFormat("json", func() {
				got = testutil.CaptureStdout(t, func() { PrintJson([]byte(tt.body)) })
			})
			assert.NotContains(t, got, "\x9b")

			var decoded map[string]string
			assert.NoError(t, json.Unmarshal([]byte(got), &decoded))
			assert.Equal(t, tt.want, decoded["name"])
		})
	}
}

func TestPrintJson_JSONOutput_EscapesFormatChars(t *testing.T) {
	// Escaped, not stripped: the terminal never receives a rune that reorders the
	// line, and a decoder still reads back the value the API sent.
	tests := []struct {
		name   string
		raw    string
		escape string
	}{
		{"bidi override", "denied\u202e\u2066 approved", `\u202e`},
		{"past the BMP", "a\U000E0001b", `\udb40\udc01`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"name": tt.raw})
			assert.NoError(t, err)

			var got string
			withFormat("json", func() {
				got = testutil.CaptureStdout(t, func() { PrintJson(body) })
			})
			assert.Contains(t, got, tt.escape)
			assert.NotContains(t, got, tt.raw)

			var decoded map[string]string
			assert.NoError(t, json.Unmarshal([]byte(got), &decoded))
			assert.Equal(t, tt.raw, decoded["name"])
		})
	}
}

func TestPrintJson_TableOutput(t *testing.T) {
	compact := []byte(`{"name":"alpha","id":1}`)
	var got string
	withFormat("table", func() {
		got = testutil.CaptureStdout(t, func() { PrintJson(compact) })
	})
	assert.Contains(t, got, "\"name\": \"alpha\"")
	assert.Contains(t, got, "\"id\": 1")
}

func TestFormatJSON_DisablesHTMLEscaping(t *testing.T) {
	t.Parallel()
	got, err := FormatJSON(map[string]string{"next": `alpacon work-session use <ID>`})
	assert.NoError(t, err)
	assert.Contains(t, got, "<ID>")
	assert.NotContains(t, got, "\\u003c")
}

func TestPrintJSONError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	PrintJSONError(&buf, JSONErrorEnvelope[map[string]string]{
		ExitCode:  3,
		ErrorCode: "work_session_required",
		Message:   "command requires a work session",
		Context: map[string]string{
			"required_scope": "command",
		},
		NextActions: []NextAction{{Command: "alpacon work-session use <ID>"}},
	})

	assert.JSONEq(t, `{
		"ok": false,
		"exit_code": 3,
		"error_code": "work_session_required",
		"message": "command requires a work session",
		"context": {"required_scope": "command"},
		"next_actions": [{"command": "alpacon work-session use <ID>"}]
	}`, buf.String())
}

// Callers write PlainText raw, outside the Cli* helpers, and interpolate
// server-returned ids into Command (#364).
func TestNextActionPlainTextSanitizesTerminalText(t *testing.T) {
	t.Parallel()
	const (
		command     = "alpacon exec logs job-1\x1b[2K\u202e"
		description = "after\x1b]0;pwn\x07 approval\u202e"
	)
	tests := []struct {
		name   string
		action NextAction
		want   string
	}{
		{
			name:   "command and description",
			action: NextAction{Command: command, Description: description},
			want:   "alpacon exec logs job-1  # after approval",
		},
		{
			name:   "command only",
			action: NextAction{Command: command},
			want:   "alpacon exec logs job-1",
		},
		{
			name:   "description only",
			action: NextAction{Description: description},
			want:   "after approval",
		},
		{
			name:   "command sanitizes to empty leaves no dangling separator",
			action: NextAction{Command: "\x1b[2K\u202e", Description: description},
			want:   "after approval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.action.PlainText())
		})
	}
}
