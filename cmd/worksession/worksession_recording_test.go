package worksession

import (
	"bytes"
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
	assert.Equal(t, 1, idx)
}

func TestFindRecording_ByIndex(t *testing.T) {
	recs := []wsapi.TimelineItem{makeRecording("r1", "s1"), makeRecording("r2", "s1"), makeRecording("r3", "s1")}
	target, idx := findRecording(recs, 3)
	assert.Equal(t, "r3", target.ID)
	assert.Equal(t, 3, idx)
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

func TestRecordingPreview_StripsBidiOverride(t *testing.T) {
	// A bidi override carries no control byte, so the control pass alone leaves
	// it free to reorder the preview a reviewer reads back.
	raw := "[user@host:~]$ echo \u202esafe"
	assert.Equal(t, "[user@host:~]$ echo safe", recordingPreview(raw))
}

func TestRecordingPreview_StripsFormatCharBuriedInSequence(t *testing.T) {
	// Format chars go before the escape strip, or the match breaks and the
	// sequence's tail lands on screen as text.
	assert.Equal(t, "ls", recordingPreview("\x1b[2\u200dKls"))
}

// printRecordingHeader

func TestPrintRecordingHeader_SanitizesTimestamp(t *testing.T) {
	// formatTimestamp returns the server's string as-is when it does not parse, so
	// the header is a sink for an escape sequence that clears the reviewer's screen.
	ts := "\x1b[2J\x1b[H\u202eSPOOFED"
	var buf bytes.Buffer
	printRecordingHeader(&buf, &wsapi.TimelineItem{Timestamp: &ts}, 1, 1)
	assert.Equal(t, "Recording 1/1 — SPOOFED\n\n", buf.String())
}

// printRecordingContent

func TestPrintRecordingContent_StripsFormatChars(t *testing.T) {
	var buf bytes.Buffer
	printRecordingContent(&buf, "echo \u202esafe\n")
	assert.Equal(t, "echo safe\n", buf.String())
}

func TestPrintRecordingContent_StripsC1Controls(t *testing.T) {
	// U+009B is an 8-bit CSI: left in, it erases the recorded line and the reviewer
	// reads what follows instead.
	var buf bytes.Buffer
	printRecordingContent(&buf, "reboot\u009b2Krm -rf /\n")
	assert.Equal(t, "reboot2Krm -rf /\n", buf.String())
}

func TestPrintRecordingContent_StripsShiftFunctions(t *testing.T) {
	// SO invokes G1 into GL and holds until SI or a reset: the same charset switch
	// as "ESC ( 0", reached without an ESC.
	var buf bytes.Buffer
	printRecordingContent(&buf, "a\x0eb\x0fc\n")
	assert.Equal(t, "abc\n", buf.String())
}

func TestPrintRecordingContent_StripsUnmatchedEscapeIntroducers(t *testing.T) {
	// ansiEscapeRE ends an ESC-led form at \x40-\x7e, so these three get past it.
	// Left in, "ESC ( 0" turns the rest of the reviewer's screen into line-drawing
	// glyphs and "ESC 7"/"ESC 8" restore the cursor onto an earlier line, which lets
	// the recording overwrite what was already read.
	for _, raw := range []string{"ab\x1b(0", "ab\x1b7\x1b8", "ab\x1b#3"} {
		var buf bytes.Buffer
		printRecordingContent(&buf, raw)
		assert.NotContains(t, buf.String(), "\x1b", "raw %q", raw)
	}
}

func TestPrintRecordingContent_KeepsControlBytes(t *testing.T) {
	// The C0 pass is deliberately absent here: a recording shown as it was keeps
	// \r and its line endings.
	var buf bytes.Buffer
	printRecordingContent(&buf, "a\rb")
	assert.Equal(t, "a\rb\n", buf.String())
}
