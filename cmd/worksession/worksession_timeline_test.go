package worksession

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseTime(ts string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return t
}

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		// RFC3339 with timezone: converted to local
		{"2024-01-15T10:30:00.123456Z", mustParseTime("2024-01-15T10:30:00.123456Z").Local().Format("2006-01-02 15:04:05")},
		{"2024-01-15T10:30:00+09:00", mustParseTime("2024-01-15T10:30:00+09:00").Local().Format("2006-01-02 15:04:05")},
		{"2024-01-15T10:30:00Z", mustParseTime("2024-01-15T10:30:00Z").Local().Format("2006-01-02 15:04:05")},
		// no timezone → fallback string manipulation
		{"2024-01-15T10:30:00", "2024-01-15 10:30:00"},
		// no T separator → return as-is
		{"2024-01-15 10:30:00", "2024-01-15 10:30:00"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatTimestamp(tc.input), tc.input)
	}
}

func TestFormatType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"command", "command"},
		{"websh_session", "websh"},
		{"tunnel_session", "tunnel"},
		{"ftp_session", "ftp"},
		{"file_upload", "upload"},
		{"file_download", "download"},
		{"sudo_grant", "sudo grant"},
		{"websh_record", "recording"},
		{"unknown_type", "unknown_type"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatType(tc.input), tc.input)
	}
}

func TestFormatSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatSize(tc.bytes), "%d bytes", tc.bytes)
	}
}

func TestSessionState(t *testing.T) {
	t.Parallel()
	closed := "2024-01-15T10:30:00Z"
	assert.Equal(t, "closed", sessionState(&closed))
	assert.Equal(t, "opened", sessionState(nil))
}

func TestFormatDetails_Command(t *testing.T) {
	t.Parallel()
	success := true
	failure := false

	tests := []struct {
		name string
		item wsapi.TimelineItem
		want string
	}{
		{
			"ok",
			wsapi.TimelineItem{Type: "command", Success: &success, Line: "ls -la"},
			"[ok] ls -la",
		},
		{
			"failed",
			wsapi.TimelineItem{Type: "command", Success: &failure, Line: "rm -rf /"},
			"[failed] rm -rf /",
		},
		{
			"denied",
			wsapi.TimelineItem{Type: "command", Denied: true, Line: "sudo su"},
			"[denied] sudo su",
		},
		{
			"unknown (nil success)",
			wsapi.TimelineItem{Type: "command", Line: "some-cmd"},
			"[unknown] some-cmd",
		},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatDetails(&tc.item), tc.name)
	}
}

func TestFormatDetails_Sessions(t *testing.T) {
	t.Parallel()
	closed := "2024-01-15T10:30:00Z"
	port := 8080

	tests := []struct {
		name string
		item wsapi.TimelineItem
		want string
	}{
		{
			"websh opened",
			wsapi.TimelineItem{Type: "websh_session"},
			"opened",
		},
		{
			"websh closed with client",
			wsapi.TimelineItem{Type: "websh_session", ClosedAt: &closed, ClientType: "vscode"},
			"closed (client: vscode)",
		},
		{
			"tunnel with port opened",
			wsapi.TimelineItem{Type: "tunnel_session", TargetPort: &port},
			"port 8080 opened",
		},
		{
			"tunnel closed",
			wsapi.TimelineItem{Type: "tunnel_session", ClosedAt: &closed},
			"closed",
		},
		{
			"ftp closed",
			wsapi.TimelineItem{Type: "ftp_session", ClosedAt: &closed},
			"closed",
		},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatDetails(&tc.item), tc.name)
	}
}

func TestFormatDetails_Files(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		item wsapi.TimelineItem
		want string
	}{
		{
			"upload",
			wsapi.TimelineItem{Type: "file_upload", Name: "report.pdf", Size: 2048},
			"↑ report.pdf (2.0 KB)",
		},
		{
			"download",
			wsapi.TimelineItem{Type: "file_download", Name: "backup.tar.gz", Size: 512},
			"↓ backup.tar.gz (512 B)",
		},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatDetails(&tc.item), tc.name)
	}
}

func TestFormatDetails_SudoGrant(t *testing.T) {
	cmd := "apt-get install vim"
	emptyCmd := ""

	tests := []struct {
		name string
		item wsapi.TimelineItem
		want string
	}{
		{
			"without command",
			wsapi.TimelineItem{Type: "sudo_grant", GrantType: "temporary", Status: "approved"},
			"temporary: approved",
		},
		{
			"with command",
			wsapi.TimelineItem{Type: "sudo_grant", GrantType: "temporary", Status: "approved", Command: &cmd},
			"temporary: approved — apt-get install vim",
		},
		{
			"empty command",
			wsapi.TimelineItem{Type: "sudo_grant", GrantType: "permanent", Status: "approved", Command: &emptyCmd},
			"permanent: approved",
		},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatDetails(&tc.item), tc.name)
	}
}

func TestFormatDetails_WebshRecord(t *testing.T) {
	t.Parallel()
	item := wsapi.TimelineItem{Type: "websh_record", MaskedRecord: "ls -la /home/user"}
	assert.Equal(t, "ls -la /home/user", formatDetails(&item))
}

func TestFormatDetails_WebshRecord_SanitizesBeforeTruncating(t *testing.T) {
	t.Parallel()
	// The escape has to go before the 60-char cut, or it eats the budget the command
	// text needs and the cell shows less than the rows beside it.
	item := wsapi.TimelineItem{Type: "websh_record", MaskedRecord: "\x1b[2K\u202els -la"}
	assert.Equal(t, "ls -la", formatDetails(&item))
}

func TestFormatDetails_Unknown(t *testing.T) {
	t.Parallel()
	item := wsapi.TimelineItem{Type: "unknown_event"}
	assert.Equal(t, "", formatDetails(&item))
}

func TestPrintTimelineTableTo_StripsControlSequences(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printTimelineTableTo(&buf, []wsapi.TimelineAttributes{{
		Time:    "2024-01-15 10:30:00",
		Type:    "websh",
		Server:  "web-01\x1b[2K\rdb-prod",
		User:    "alice\nroot",
		Details: "opened",
	}})

	got := buf.String()
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\r")
	assert.Equal(t, 2, strings.Count(got, "\n"), "header and one row only — a newline must not forge a timeline entry")
	assert.Contains(t, got, "web-01db-prod")
	assert.Contains(t, got, "aliceroot")
}

func TestPrintRecordingsSectionTo_StripsControlSequences(t *testing.T) {
	t.Parallel()
	serverID := "srv-1"
	timestamp := "2024-01-15T10:30:00Z"
	var buf bytes.Buffer
	printRecordingsSectionTo(&buf, []wsapi.TimelineItem{{
		Type:      "websh_record",
		Timestamp: &timestamp,
		ServerID:  &serverID,
	}}, map[string]string{serverID: "web-01\x1b[2K\rdb-prod"})

	got := buf.String()
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\r")
	assert.Contains(t, got, "web-01db-prod")
}

// A bidi override in a server name reaches the terminal through --output json
// too, so the JSON goes out escaped rather than verbatim.
func TestOutputTimelineJSON_EscapesFormatChars(t *testing.T) {
	rows := []wsapi.TimelineAttributes{{Server: "prod\u202edb"}}
	out := testutil.CaptureStdout(t, func() {
		outputTimelineJSON(rows, nil, nil)
	})
	assert.NotContains(t, out, "\u202e")
	assert.Contains(t, out, `\u202e`)
}

// outputTimelineJSON must emit recordings as [] not null when no recordings exist,
// including when --no-records passes nil for recordings.
func TestOutputTimelineJSON_RecordingsEmptyArrayNotNull(t *testing.T) {
	out := testutil.CaptureStdout(t, func() {
		outputTimelineJSON([]wsapi.TimelineAttributes{}, nil, nil)
	})
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "[]", string(result["recordings"]), "recordings must be [] not null")
}

// Both timeline and recordings keys must always be present in JSON output.
func TestOutputTimelineJSON_BothKeysPresent(t *testing.T) {
	out := testutil.CaptureStdout(t, func() {
		outputTimelineJSON([]wsapi.TimelineAttributes{}, nil, nil)
	})
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &keys))
	_, hasTimeline := keys["timeline"]
	_, hasRecordings := keys["recordings"]
	assert.True(t, hasTimeline, "timeline key must be present in JSON output")
	assert.True(t, hasRecordings, "recordings key must be present in JSON output")
}
