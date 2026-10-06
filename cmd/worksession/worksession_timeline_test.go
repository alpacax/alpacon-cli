package worksession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
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
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, formatTimestamp(tc.input))
		})
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
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, formatType(tc.input))
		})
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
		t.Run(fmt.Sprintf("%d bytes", tc.bytes), func(t *testing.T) {
			assert.Equal(t, tc.want, formatSize(tc.bytes))
		})
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
			"unknown nil success",
			wsapi.TimelineItem{Type: "command", Line: "some-cmd"},
			"[unknown] some-cmd",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatDetails(&tc.item))
		})
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
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatDetails(&tc.item))
		})
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
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatDetails(&tc.item))
		})
	}
}

func TestFormatDetails_SudoGrant(t *testing.T) {
	t.Parallel()
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
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatDetails(&tc.item))
		})
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
	assert.Empty(t, formatDetails(&item))
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

// --no-records has to reach the request, not only the renderer: the flag exists
// so the recording bytes are never downloaded, and include_records is the only
// thing that stops the server sending them. The helper runs the real command in
// a subprocess, so what is asserted is what the flag puts on the wire.
//
// The fake honors include_records the way the server does, so reverting the fix
// moves both assertions: the parameter arrives as "true" and the records come
// back with it. The last case is a server too old to know the parameter: it
// sends the records anyway, and the flag has to hold there too.
func TestTimelineNoRecordsIsOnTheWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		args           []string
		ignoresParam   bool
		wantParam      string
		wantRecordings bool
	}{
		{"default asks for the records", []string{"timeline", "ses-1"}, false, "true", true},
		{"no-records does not", []string{"timeline", "ses-1", "--no-records"}, false, "false", false},
		{"no-records holds on an old server", []string{"timeline", "ses-1", "--no-records"}, true, "false", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var (
				mu       sync.Mutex
				gotParam string
			)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/api/work-sessions/sessions/ses-1/timeline/" {
					_, _ = w.Write([]byte(`{"id":"ses-1","status":"active","servers":[{"id":"srv-1","name":"web-01"}]}`))
					return
				}
				param := r.URL.Query().Get("include_records")
				mu.Lock()
				gotParam = param
				mu.Unlock()
				// The client sends strconv.FormatBool output, so "false" is the
				// only falsy spelling that can arrive here.
				results := `{"type":"websh_session","id":"wss-1","server_id":"srv-1","timestamp":"2024-01-15T10:30:00Z"}`
				if param != "false" || tc.ignoresParam {
					results += `,{"type":"websh_record","session_id":"wss-1","server_id":"srv-1","timestamp":"2024-01-15T10:30:01Z","masked_record":"ls -la"}`
				}
				_, _ = w.Write([]byte(`{"count":2,"results":[` + results + `]}`))
			}))
			defer ts.Close()

			stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL, tc.args...)
			require.Equal(t, 0, exitCode, "stderr: %s", stderr)

			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tc.wantParam, gotParam)

			if tc.wantRecordings {
				assert.Contains(t, stdout, "• 1 recording")
				assert.Contains(t, stdout, "Recordings (1)")
			} else {
				// The badge goes with the bytes: its count rides on the recording
				// items, so an unrequested recording cannot be counted either.
				assert.NotContains(t, stdout, "recording")
				assert.NotContains(t, stdout, "Recordings")
			}
		})
	}
}

// The help text has to describe what the flag really does. Dropping the request
// also drops the per-row recording count, and a help text that promised only to
// hide the section below the timeline would overstate what survives.
func TestNoRecordsFlagHelpDescribesTheCount(t *testing.T) {
	// Reads a package-level Cobra command, so it stays serial.
	flag := workSessionTimelineCmd.Flags().Lookup("no-records")
	require.NotNil(t, flag)
	assert.Contains(t, flag.Usage, "recording count")
}
