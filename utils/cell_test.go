package utils

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimestampKeepsRawTimeAndTableText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cell Timestamp
		json string
		text string
	}{
		{"non UTC", NewTimestamp(time.Date(2026, 11, 7, 18, 30, 45, 0, time.FixedZone("UTC+9", 9*60*60)), "3 days ago"), `"2026-11-07T09:30:45Z"`, "3 days ago"},
		{"zero", NewTimestamp(time.Time{}, "None"), `null`, "None"},
		{"zero value", Timestamp{}, `null`, ""},
		{"relative zero", RelativeTime(time.Time{}), `null`, "None"},
		{"fractional seconds kept", NewTimestamp(time.Date(2026, 11, 7, 9, 30, 45, 123456000, time.UTC), "x"), `"2026-11-07T09:30:45.123456Z"`, "x"},
		{"parsed offset string", ParseTimestamp("2026-11-07T18:30:45+09:00"), `"2026-11-07T09:30:45Z"`, "2026-11-07T18:30:45+09:00"},
		{"parsed fractional string", ParseTimestamp("2026-11-07T09:30:45.5Z"), `"2026-11-07T09:30:45.5Z"`, "2026-11-07T09:30:45.5Z"},
		{"unparsed", ParseTimestamp("unknown time"), `"unknown time"`, "unknown time"},
		{"empty string", ParseTimestamp(""), `null`, ""},
		{"optional nil", OptionalTimestamp(nil), `null`, ""},
		{"optional set", OptionalTimestamp(&[]time.Time{time.Date(2026, 11, 7, 18, 30, 45, 0, time.FixedZone("UTC+9", 9*60*60))}[0]), `"2026-11-07T09:30:45Z"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(tt.cell)
			require.NoError(t, err)
			assert.JSONEq(t, tt.json, string(body))
			assert.Equal(t, tt.text, tt.cell.String())
		})
	}
}

func TestTruncatedTextKeepsFullJSONAndTruncatesTableText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		full  string
		width int
		text  string
	}{
		{"long", "abcdef", 3, "abc..."},
		{"exact width", "abc", 3, "abc"},
		{"multi byte", "가나다라마", 3, "가나다..."},
		{"multi byte exact width", "가나다", 3, "가나다"},
		{"empty", "", 3, ""},
		{"zero width", "abc", 0, "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cell := Truncate(tt.full, tt.width)
			body, err := json.Marshal(cell)
			require.NoError(t, err)
			var full string
			require.NoError(t, json.Unmarshal(body, &full))
			assert.Equal(t, tt.full, full)
			assert.Equal(t, tt.text, cell.String())
		})
	}
}

func TestPrintTableUsesCellTableTextAndRawJSON(t *testing.T) {
	rows := []struct {
		Time Timestamp     `json:"time"`
		Text TruncatedText `json:"text"`
	}{{NewTimestamp(time.Date(2026, 11, 7, 18, 30, 45, 0, time.FixedZone("UTC+9", 9*60*60)), "3 days ago"), Truncate("abcdef", 3)}}
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{"table", OutputFormatTable, "TIME         TEXT\n3 days ago   abc...\n"},
		{"json", OutputFormatJSON, `[{"time":"2026-11-07T09:30:45Z","text":"abcdef"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			withFormat(tt.format, func() {
				got = testutil.CaptureStdout(t, func() { PrintTable(rows) })
			})
			if tt.format == OutputFormatJSON {
				assert.JSONEq(t, tt.want, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
