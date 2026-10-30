package worksession

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// printRecordingHeader

func TestPrintRecordingHeader_SanitizesTimestamp(t *testing.T) {
	t.Parallel()
	// formatTimestamp returns the server's string as-is when it does not parse, so
	// the header is a sink for an escape sequence that clears the reviewer's screen.
	ts := "\x1b[2J\x1b[H\u202eSPOOFED"
	var buf bytes.Buffer
	printRecordingHeader(&buf, ts, 1, 1)
	assert.Equal(t, "Terminal 1/1 — SPOOFED\n\n", buf.String())
}

// printRecordingContent

func TestPrintRecordingContent_StripsFormatChars(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printRecordingContent(&buf, "echo \u202esafe\n")
	assert.Equal(t, "echo safe\n", buf.String())
}

func TestPrintRecordingContent_StripsC1Controls(t *testing.T) {
	t.Parallel()
	// U+009B is an 8-bit CSI: left in, it erases the recorded line and the reviewer
	// reads what follows instead.
	var buf bytes.Buffer
	printRecordingContent(&buf, "reboot\u009b2Krm -rf /\n")
	assert.Equal(t, "reboot2Krm -rf /\n", buf.String())
}

func TestPrintRecordingContent_StripsRawC1Byte(t *testing.T) {
	t.Parallel()
	// A recording of an 8-bit program carries CSI as the raw byte 0x9b, which is
	// invalid UTF-8, so IsC1OrDEL never sees it. What keeps it off the terminal is
	// strings.Map substituting U+FFFD, so a rewrite into a byte loop reopens this.
	var buf bytes.Buffer
	printRecordingContent(&buf, "reboot\x9b2Krm -rf /\n")
	assert.Equal(t, "reboot�2Krm -rf /\n", buf.String())
}

func TestPrintRecordingContent_StripsShiftFunctions(t *testing.T) {
	t.Parallel()
	// SO invokes G1 into GL and holds until SI or a reset: the same charset switch
	// as "ESC ( 0", reached without an ESC.
	var buf bytes.Buffer
	printRecordingContent(&buf, "a\x0eb\x0fc\n")
	assert.Equal(t, "abc\n", buf.String())
}

func TestPrintRecordingContent_StripsUnmatchedEscapeIntroducers(t *testing.T) {
	t.Parallel()
	// ansiEscapeRE ends an ESC-led form at \x40-\x7e, so these three get past it.
	// "ESC 7"/"ESC 8" restore the cursor onto an earlier line, which lets the
	// recording overwrite what the reviewer already read.
	for _, raw := range []string{"ab\x1b(0", "ab\x1b7\x1b8", "ab\x1b#3"} {
		var buf bytes.Buffer
		printRecordingContent(&buf, raw)
		assert.NotContains(t, buf.String(), "\x1b", "raw %q", raw)
	}
}

func TestPrintRecordingContent_KeepsControlBytes(t *testing.T) {
	t.Parallel()
	// The C0 pass is deliberately absent here: a recording shown as it was keeps
	// \r and its line endings.
	var buf bytes.Buffer
	printRecordingContent(&buf, "a\rb")
	assert.Equal(t, "a\rb\n", buf.String())
}
