package worksession

import (
	"strings"
	"testing"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/stretchr/testify/assert"
)

func makeRecording(id, sessionID string) wsapi.TimelineItem {
	return wsapi.TimelineItem{Type: "websh_record", ID: id, SessionID: sessionID}
}

// findRecording

func TestFindRecording_DefaultFirst(t *testing.T) {
	recs := []wsapi.TimelineItem{makeRecording("r1", "s1"), makeRecording("r2", "s1")}
	target, idx := findRecording(recs, 1)
	assert.Equal(t, "r1", target.ID)
	assert.Equal(t, 0, idx)
}

func TestFindRecording_ByIndex(t *testing.T) {
	recs := []wsapi.TimelineItem{makeRecording("r1", "s1"), makeRecording("r2", "s1"), makeRecording("r3", "s1")}
	target, idx := findRecording(recs, 3)
	assert.Equal(t, "r3", target.ID)
	assert.Equal(t, 2, idx)
}

func TestFindRecording_IndexOutOfRange(t *testing.T) {
	recs := []wsapi.TimelineItem{makeRecording("r1", "s1")}
	target, idx := findRecording(recs, 2)
	assert.Nil(t, target)
	assert.Equal(t, -1, idx)
}

func TestFindRecording_IndexZero(t *testing.T) {
	recs := []wsapi.TimelineItem{makeRecording("r1", "s1")}
	target, idx := findRecording(recs, 0)
	assert.Nil(t, target)
	assert.Equal(t, -1, idx)
}

func TestFindRecording_NegativeIndex(t *testing.T) {
	recs := []wsapi.TimelineItem{makeRecording("r1", "s1")}
	target, idx := findRecording(recs, -1)
	assert.Nil(t, target)
	assert.Equal(t, -1, idx)
}

// buildRecordingIndex

func TestBuildRecordingIndex_GroupsBySessionID(t *testing.T) {
	items := []wsapi.TimelineItem{
		{Type: "websh_session", ID: "s1"},
		{Type: "websh_record", ID: "r1", SessionID: "s1"},
		{Type: "websh_record", ID: "r2", SessionID: "s1"},
		{Type: "websh_session", ID: "s2"},
		{Type: "websh_record", ID: "r3", SessionID: "s2"},
	}
	bySession, flat := buildRecordingIndex(items)
	assert.Len(t, bySession["s1"], 2)
	assert.Len(t, bySession["s2"], 1)
	assert.Len(t, flat, 3)
	assert.Equal(t, "r1", bySession["s1"][0].ID)
	assert.Equal(t, "r3", bySession["s2"][0].ID)
}

func TestBuildRecordingIndex_NoRecordings(t *testing.T) {
	items := []wsapi.TimelineItem{
		{Type: "websh_session", ID: "s1"},
		{Type: "ftp_session", ID: "f1"},
	}
	bySession, flat := buildRecordingIndex(items)
	assert.Empty(t, bySession)
	assert.Empty(t, flat)
}

func TestBuildRecordingIndex_Empty(t *testing.T) {
	bySession, flat := buildRecordingIndex(nil)
	assert.Empty(t, bySession)
	assert.Empty(t, flat)
}

// recordingBadge

func TestRecordingBadge_Single(t *testing.T) {
	assert.Equal(t, "• 1 recording", recordingBadge(1))
}

func TestRecordingBadge_Multiple(t *testing.T) {
	assert.Equal(t, "• 3 recordings", recordingBadge(3))
}

// recordingPreview

func TestRecordingPreview_StripsANSI(t *testing.T) {
	raw := "\x1b]0;user@host:~\x07\x1b[?2004h[user@host:~]$ ls -la"
	assert.Equal(t, "[user@host:~]$ ls -la", recordingPreview(raw))
}

func TestRecordingPreview_StripsCarriageReturns(t *testing.T) {
	raw := "[user@host:~]$ \r\r[user@host:~]$ ls -la"
	assert.Equal(t, "[user@host:~]$ ls -la", recordingPreview(raw))
}

func TestRecordingPreview_SkipsEmptyLines(t *testing.T) {
	assert.Equal(t, "actual content here", recordingPreview("\n\n  \nactual content here"))
}

func TestRecordingPreview_Truncates(t *testing.T) {
	raw := strings.Repeat("a", 80)
	assert.LessOrEqual(t, len(recordingPreview(raw)), 63) // 60 chars + possible "..."
}

func TestRecordingPreview_EmptyRaw(t *testing.T) {
	assert.Equal(t, "", recordingPreview(""))
}

func TestRecordingPreview_OnlyANSI(t *testing.T) {
	assert.Equal(t, "", recordingPreview("\x1b[?2004h\x1b[2J\x1b[H"))
}

