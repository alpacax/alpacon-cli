package utils

import (
	"encoding/json"
	"time"
)

// Timestamp is a list cell: its table text in a table, RFC 3339 UTC or null in JSON,
// or the server's string as sent when it could not be parsed.
type Timestamp struct {
	time      time.Time
	tableText string
	raw       *string
}

func RelativeTime(t time.Time) Timestamp {
	return NewTimestamp(t, TimeUtils(t))
}

func NewTimestamp(t time.Time, tableText string) Timestamp {
	return Timestamp{time: t, tableText: tableText}
}

// OptionalTimestamp is an absolute time with no table text, null in JSON when t is nil.
func OptionalTimestamp(t *time.Time) Timestamp {
	if t == nil {
		return Timestamp{}
	}
	return NewTimestamp(*t, "")
}

// ParseTimestamp shows a server's time string as sent in a table. JSON gets it in UTC,
// null when empty, or as sent when it is not RFC 3339.
func ParseTimestamp(value string) Timestamp {
	return parseTimestamp(value, value)
}

// ParseLocalTimestamp is ParseTimestamp with the table text in the local zone (FormatTimestamp).
func ParseLocalTimestamp(value string) Timestamp {
	return parseTimestamp(value, FormatTimestamp(value))
}

func parseTimestamp(value, tableText string) Timestamp {
	if value == "" {
		return Timestamp{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return Timestamp{time: parsed, tableText: tableText}
	}
	return Timestamp{tableText: tableText, raw: &value}
}

func (t Timestamp) String() string {
	return t.tableText
}

func (t Timestamp) MarshalJSON() ([]byte, error) {
	if t.raw != nil {
		return json.Marshal(t.raw)
	}
	if t.time.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.time.UTC().Format(time.RFC3339Nano))
}

// TruncatedText is a list cell cut to width in a table and whole in JSON.
type TruncatedText struct {
	full  string
	width int
}

func Truncate(s string, width int) TruncatedText {
	return TruncatedText{full: s, width: width}
}

func (t TruncatedText) String() string {
	return TruncateString(t.full, t.width)
}

func (t TruncatedText) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.full)
}
