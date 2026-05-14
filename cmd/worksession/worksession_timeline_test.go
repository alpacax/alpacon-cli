package worksession

import (
	"testing"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/stretchr/testify/assert"
)

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2024-01-15T10:30:00.123456Z", "2024-01-15 10:30:00"},
		{"2024-01-15T10:30:00+09:00", "2024-01-15 10:30:00"},
		{"2024-01-15T10:30:00Z", "2024-01-15 10:30:00"},
		{"2024-01-15T10:30:00", "2024-01-15 10:30:00"},
		{"2024-01-15 10:30:00", "2024-01-15 10:30:00"}, // no T — return as-is
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatTimestamp(tc.input), tc.input)
	}
}

func TestFormatType(t *testing.T) {
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
	closed := "2024-01-15T10:30:00Z"
	assert.Equal(t, "closed", sessionState(&closed))
	assert.Equal(t, "opened", sessionState(nil))
}

func TestFormatDetails_Command(t *testing.T) {
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
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatDetails(&tc.item), tc.name)
	}
}

func TestFormatDetails_Sessions(t *testing.T) {
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
	item := wsapi.TimelineItem{Type: "websh_record", MaskedRecord: "ls -la /home/user"}
	assert.Equal(t, "ls -la /home/user", formatDetails(&item))
}

func TestFormatDetails_Unknown(t *testing.T) {
	item := wsapi.TimelineItem{Type: "unknown_event"}
	assert.Equal(t, "", formatDetails(&item))
}
