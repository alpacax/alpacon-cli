package exec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExceedsInlineLimit(t *testing.T) {
	assert.False(t, exceedsInlineLimit(""))
	assert.False(t, exceedsInlineLimit(strings.Repeat("a", 2048)), "2048 bytes is the inclusive inline boundary")
	assert.True(t, exceedsInlineLimit(strings.Repeat("a", 2049)))
}

func TestTempScriptName(t *testing.T) {
	assert.Equal(t, ".alpacon-exec-abc123.sh", tempScriptName("abc123"))
}

func TestTempScriptPath(t *testing.T) {
	assert.Equal(t, "/tmp/.alpacon-exec-abc123.sh", tempScriptPath("abc123"))
}

func TestWrapScriptCommand(t *testing.T) {
	assert.Equal(t,
		"sh /tmp/x.sh; rc=$?; rm -f /tmp/x.sh; exit $rc",
		wrapScriptCommand("/tmp/x.sh"))
}

func TestIsWindowsPlatform(t *testing.T) {
	assert.True(t, isWindowsPlatform("windows"))
	assert.True(t, isWindowsPlatform("Windows"))
	assert.True(t, isWindowsPlatform("  windows  "))
	assert.False(t, isWindowsPlatform("debian"))
	assert.False(t, isWindowsPlatform("rhel"))
	assert.False(t, isWindowsPlatform("darwin"))
	assert.False(t, isWindowsPlatform(""), "empty/unknown platform proceeds as POSIX")
}

func TestNewExecID(t *testing.T) {
	id := newExecID()
	assert.Len(t, id, 16, "8 random bytes hex-encoded")
	assert.Regexp(t, "^[0-9a-f]+$", id, "id is lowercase hex")
	assert.NotEqual(t, newExecID(), id, "ids should differ between calls")
}
