package worksession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		Type:      "websh_session",
		ID:        "wsh-1",
		Timestamp: &timestamp,
		ServerID:  &serverID,
	}}, "ses-1\x1b[2J", map[string]string{serverID: "web-01\x1b[2K\rdb-prod"})

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
		outputTimelineJSON(rows, nil, "ses-1", nil)
	})
	assert.NotContains(t, out, "\u202e")
	assert.Contains(t, out, `\u202e`)
}

// outputTimelineJSON must emit recordings as [] not null when the session has no
// terminal.
func TestOutputTimelineJSON_RecordingsEmptyArrayNotNull(t *testing.T) {
	out := testutil.CaptureStdout(t, func() {
		outputTimelineJSON([]wsapi.TimelineAttributes{}, nil, "ses-1", nil)
	})
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "[]", string(result["recordings"]), "recordings must be [] not null")
}

// Both timeline and recordings keys must always be present in JSON output.
func TestOutputTimelineJSON_BothKeysPresent(t *testing.T) {
	out := testutil.CaptureStdout(t, func() {
		outputTimelineJSON([]wsapi.TimelineAttributes{}, nil, "ses-1", nil)
	})
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &keys))
	_, hasTimeline := keys["timeline"]
	_, hasRecordings := keys["recordings"]
	assert.True(t, hasTimeline, "timeline key must be present in JSON output")
	assert.True(t, hasRecordings, "recordings key must be present in JSON output")
}

func TestRecordingStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		total *int
		want  string
	}{
		{"several chunks", new(3), "3 chunks"},
		{"one chunk", new(1), "1 chunk"},
		{"no recording", new(0), "no recording"},
		{"count unknown", nil, "unknown"},
		{"negative count", new(-1), "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, recordingStatus(tc.total))
		})
	}
}

// timelinePagingServer answers the paged timeline in two pages and logs every
// request. Its terminals, in order, count 3 chunks, 0, null, no key at all, and
// 1; the tunnel rows between them carry a count a terminal filter must not see,
// and the websh_record row is what a server too old for include_records embeds.
func timelinePagingServer(t *testing.T) (*httptest.Server, func() []*url.URL) {
	t.Helper()
	var (
		mu  sync.Mutex
		log []*url.URL
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		log = append(log, r.URL)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/work-sessions/sessions/ses-1/":
			_, _ = w.Write([]byte(`{"id":"ses-1","status":"active","servers":[{"id":"srv-1","name":"web-01"}]}`))
		case "/api/work-sessions/sessions/ses-1/timeline/":
			if r.URL.Query().Get("cursor") == "" {
				_, _ = w.Write([]byte(`{"next":"p2","results":[
					{"type":"command","id":"c1","server_id":"srv-1","timestamp":"2024-01-15T10:30:00Z","line":"ls"},
					{"type":"websh_session","id":"wsh-1","server_id":"srv-1","timestamp":"2024-01-15T10:30:01Z","record_chunks_total":3,"max_cols":120,"max_rows":40},
					{"type":"websh_record","id":"rec-1","session_id":"wsh-1","server_id":"srv-1","timestamp":"2024-01-15T10:30:01Z","record":"$ ls\r\n"},
					{"type":"websh_session","id":"wsh-2","server_id":"srv-1","timestamp":"2024-01-15T10:30:02Z","record_chunks_total":0}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"next":null,"results":[
				{"type":"tunnel_session","id":"tun-1","server_id":"srv-1","timestamp":"2024-01-15T10:30:03Z","is_tunnel":true,"target_port":5432},
				{"type":"websh_session","id":"wsh-t","server_id":"srv-1","timestamp":"2024-01-15T10:30:04Z","is_tunnel":true,"record_chunks_total":7},
				{"type":"websh_session","id":"wsh-3","server_id":"srv-1","timestamp":"2024-01-15T10:30:05Z","record_chunks_total":null},
				{"type":"websh_session","id":"wsh-4","server_id":"srv-1","timestamp":"2024-01-15T10:30:06Z"},
				{"type":"websh_session","id":"wsh-5","server_id":"srv-1","timestamp":"2024-01-15T10:30:07Z","record_chunks_total":1}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, func() []*url.URL {
		mu.Lock()
		defer mu.Unlock()
		return append([]*url.URL(nil), log...)
	}
}

// The default timeline reads by cursor: it names an empty cursor first, follows
// `next`, asks every page to leave the recordings out, reads no recording, and
// drops the recording an old server embeds anyway.
func TestTimelineDefaultWalksTheCursorAndFetchesNoRecording(t *testing.T) {
	t.Parallel()
	ts, requests := timelinePagingServer(t)

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL, "timeline", "ses-1")
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stdout, "[unknown] ls", "the first page must be rendered")
	assert.Contains(t, stdout, "port 5432 opened", "the second page must be rendered")
	assert.NotContains(t, stdout, "websh_record", "an embedded recording is not a timeline row")

	var timeline []*url.URL
	for _, u := range requests() {
		assert.NotContains(t, u.Path, "/records/", "timeline must not read a recording")
		if strings.HasSuffix(u.Path, "/timeline/") {
			assert.Equal(t, "false", u.Query().Get("include_records"))
			timeline = append(timeline, u)
		}
	}
	require.Len(t, timeline, 2)
	assert.True(t, timeline[0].Query().Has("cursor"), "first request must name cursor, sent %q", timeline[0].RawQuery)
	assert.Empty(t, timeline[0].Query().Get("cursor"))
	assert.Equal(t, "p2", timeline[1].Query().Get("cursor"))
}

func TestTimelineRecordingsSectionReadsTheChunkCount(t *testing.T) {
	t.Parallel()
	ts, _ := timelinePagingServer(t)

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL, "timeline", "ses-1")
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)

	_, section, found := strings.Cut(stdout, "─── Recordings (5 terminals)")
	require.True(t, found, "stdout: %s", stdout)
	lines := strings.Split(strings.TrimSpace(section), "\n")[1:] // drop the rest of the rule
	hint := func(n int) string { return fmt.Sprintf("     alpacon work-session recording ses-1 --index %d", n) }
	want := []struct {
		timestamp, status string
		hint              bool
	}{
		{"2024-01-15T10:30:01Z", "3 chunks", true},
		{"2024-01-15T10:30:02Z", "no recording", false},
		{"2024-01-15T10:30:05Z", "unknown", true},
		{"2024-01-15T10:30:06Z", "unknown", true},
		{"2024-01-15T10:30:07Z", "1 chunk", true},
	}
	i := 0
	for n, w := range want {
		require.Less(t, i, len(lines), "section ended before [%d]:\n%s", n+1, section)
		assert.Equal(t, fmt.Sprintf("[%d]  %s  web-01  %s", n+1, formatTimestamp(w.timestamp), w.status), lines[i])
		i++
		if w.hint {
			require.Less(t, i, len(lines))
			assert.Equal(t, hint(n+1), lines[i])
			i++
		}
	}
	assert.Len(t, lines, i, "section has extra lines:\n%s", section)
	assert.NotContains(t, stdout, "7 chunks", "a tunnel is not a terminal")
}

func TestTimelineJSONRecordingsReadTheChunkCount(t *testing.T) {
	t.Parallel()
	ts, _ := timelinePagingServer(t)

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL, "timeline", "ses-1")
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var out struct {
		Recordings json.RawMessage `json:"recordings"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out), "stdout: %s", stdout)
	assert.NotContains(t, stdout, "websh_record", "an embedded recording is not a timeline row")
	entry := func(index int, id, timestamp, total, status string) string {
		return fmt.Sprintf(`{"index":%d,"session_id":%q,"time":%q,"server":"web-01","record_chunks_total":%s,"status":%q}`,
			index, id, formatTimestamp(timestamp), total, status)
	}
	assert.JSONEq(t, "["+strings.Join([]string{
		entry(1, "wsh-1", "2024-01-15T10:30:01Z", "3", "3 chunks"),
		entry(2, "wsh-2", "2024-01-15T10:30:02Z", "0", "no recording"),
		entry(3, "wsh-3", "2024-01-15T10:30:05Z", "null", "unknown"),
		entry(4, "wsh-4", "2024-01-15T10:30:06Z", "null", "unknown"),
		entry(5, "wsh-5", "2024-01-15T10:30:07Z", "1", "1 chunk"),
	}, ",")+"]", string(out.Recordings))
}

// --no-records keeps the contract scripts rely on: the section is left out of
// the table, and JSON carries an empty recordings list.
func TestTimelineNoRecordsHidesTheSection(t *testing.T) {
	t.Parallel()
	ts, _ := timelinePagingServer(t)

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL, "timeline", "ses-1", "--no-records")
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stdout, "TIME")
	assert.NotContains(t, stdout, "Recordings (")
	assert.NotContains(t, stdout, "work-session recording")
	assert.Empty(t, stderr)

	stdout, stderr, exitCode = runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL, "timeline", "ses-1", "--no-records")
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	var out struct {
		Timeline   []json.RawMessage `json:"timeline"`
		Recordings json.RawMessage   `json:"recordings"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.NotEmpty(t, out.Timeline)
	assert.JSONEq(t, `[]`, string(out.Recordings))
}
