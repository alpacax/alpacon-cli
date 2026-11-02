package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
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

// Serial: swaps os.Stderr and seals the process-wide stderr.
func TestSealedStderrDropsLaterNotices(t *testing.T) {
	t.Cleanup(func() {
		stderrMu.Lock()
		stderrSealed = false
		stderrMu.Unlock()
	})

	_, stderr := testutil.CaptureOutput(t, func() {
		CliWarning("before")
		sealStderr()
		CliWarning("after")
	})

	assert.Contains(t, stderr, "before")
	assert.NotContains(t, stderr, "after")
}

func TestExitWithFailureJSONStaysLastWhileAGoroutinePrints(t *testing.T) {
	t.Parallel()
	helper := osexec.Command(os.Args[0], "-test.run=^TestExitWithFailureJSONNoisyHelperProcess$")
	helper.Env = append(os.Environ(), "GO_WANT_NOISY_EXIT_HELPER=1")
	var stdout, stderr bytes.Buffer
	helper.Stdout, helper.Stderr = &stdout, &stderr

	err := helper.Run()

	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, ExitCodeServerBusy, exitErr.ExitCode())
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "still polling")
	lastLineIsEnvelope(t, stderr.String())
}

// Serial: the child reaches an os.Exit.
func TestExitWithFailureJSONNoisyHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_NOISY_EXIT_HELPER") == "" {
		return
	}
	OutputFormat = OutputFormatJSON
	printed := make(chan struct{})
	go func() {
		CliWarning("still polling")
		close(printed)
		for {
			CliWarning("still polling")
		}
	}()
	<-printed
	ExitWithFailureJSON(Failure{ExitCode: ExitCodeServerBusy, Err: errors.New("busy"), Message: "busy"})
}
