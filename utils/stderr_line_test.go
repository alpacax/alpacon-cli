package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lastLineIsEnvelope(t *testing.T, stderr string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	var env JSONErrorEnvelope[cliErrorCtx]
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &env), "stderr: %q", stderr)
	assert.False(t, env.OK)
}

// Serial: swaps os.Stderr and reads the process-wide line state.
func TestPrintJSONErrorStartsOnItsOwnLine(t *testing.T) {
	tests := []struct {
		name      string
		write     string
		wantLines int
		wantHead  string
	}{
		{"after a prompt left open", "Username: ", 2, "Username: \n"},
		{"after unterminated output", "fail", 2, "fail\n"},
		{"at line start", "Warning: slow\n", 2, "Warning: slow\n"},
		{"with nothing written", "", 1, ""},
	}
	for _, tt := range tests {
		t.Run(strings.ReplaceAll(tt.name, " ", "_"), func(t *testing.T) {
			stderrMidLine.Store(false)
			t.Cleanup(func() { stderrMidLine.Store(false) })

			_, stderr := testutil.CaptureOutput(t, func() {
				_, _ = fmt.Fprint(TrackedStderr(), tt.write)
				PrintJSONError(os.Stderr, JSONErrorEnvelope[cliErrorCtx]{ExitCode: 1, ErrorCode: ErrorCodeCLI, Message: "boom"})
			})

			assert.True(t, strings.HasPrefix(stderr, tt.wantHead), "stderr: %q", stderr)
			assert.Len(t, strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"), tt.wantLines)
			lastLineIsEnvelope(t, stderr)
		})
	}
}

// Serial: swaps os.Stderr and reads the process-wide line state.
func TestPrintJSONErrorLeavesStderrAtLineStart(t *testing.T) {
	stderrMidLine.Store(false)
	t.Cleanup(func() { stderrMidLine.Store(false) })

	_, stderr := testutil.CaptureOutput(t, func() {
		_, _ = fmt.Fprint(TrackedStderr(), "Username: ")
		PrintJSONError(os.Stderr, JSONErrorEnvelope[cliErrorCtx]{ErrorCode: ErrorCodeCLI})
		PrintJSONError(os.Stderr, JSONErrorEnvelope[cliErrorCtx]{ErrorCode: ErrorCodeCLI})
	})

	assert.NotContains(t, stderr, "\n\n")
}

// Serial: swaps os.Stderr and reads the process-wide line state.
func TestPrintJSONErrorStartsOnItsOwnLineAfterAChild(t *testing.T) {
	stderrMidLine.Store(false)
	t.Cleanup(func() { stderrMidLine.Store(false) })

	_, stderr := testutil.CaptureOutput(t, func() {
		_, _ = fmt.Fprint(StderrForChild(), "fail")
		PrintJSONError(os.Stderr, JSONErrorEnvelope[cliErrorCtx]{ExitCode: 1, ErrorCode: ErrorCodeCLI, Message: "boom"})
	})

	assert.True(t, strings.HasPrefix(stderr, "fail\n"), "stderr: %q", stderr)
	lastLineIsEnvelope(t, stderr)
}
