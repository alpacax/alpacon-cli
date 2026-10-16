package event

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEventTargetAllowsOmittedTarget(t *testing.T) {
	t.Parallel()

	target, err := validateEventTarget("")

	require.NoError(t, err)
	assert.Empty(t, target)
}

func TestValidateEventTargetRejectsInvalidID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target string
	}{
		{name: "whitespace only", target: "   "},
		{name: "parent traversal", target: "../x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := validateEventTarget(tc.target)

			require.ErrorIs(t, err, utils.ErrInvalidID)
			assert.Empty(t, got)
		})
	}
}

func TestValidateEventTargetReturnsTrimmedID(t *testing.T) {
	t.Parallel()

	target, err := validateEventTarget("  session-id  ")

	require.NoError(t, err)
	assert.Equal(t, "session-id", target)
}

func TestRunWaitAndWatch_RefuseAnInvalidTargetAsAUsageError(t *testing.T) {
	for _, command := range []string{opWait, opWatch} {
		t.Run(command, func(t *testing.T) {
			helper := exec.Command(os.Args[0], "-test.run=^TestEventTargetHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_EVENT_TARGET_HELPER="+command, "HOME="+t.TempDir())
			var stderr bytes.Buffer
			helper.Stderr = &stderr

			err := helper.Run()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, utils.ExitCodeUsageError, exitErr.ExitCode(), "stderr: %s", stderr.String())
			var envelope struct {
				ErrorCode string `json:"error_code"`
				Message   string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
			assert.Equal(t, utils.UsageErrorCode, envelope.ErrorCode)
			assert.Contains(t, envelope.Message, "invalid target ID")
		})
	}
}

// A valid --type and an empty HOME make any path past the target check fail
// differently: a missing type exits 2 too, while a missing login exits 1.
func TestEventTargetHelperProcess(t *testing.T) {
	command := os.Getenv("GO_WANT_EVENT_TARGET_HELPER")
	if command == "" {
		return
	}
	utils.OutputFormat = utils.OutputFormatJSON
	cmd, run := waitCmd, runWait
	if command == opWatch {
		cmd, run = watchCmd, runWatch
	}
	_ = cmd.Flags().Set("type", "command")
	_ = cmd.Flags().Set("target", "../x")
	run(cmd, nil)
	os.Exit(0)
}

func TestExitNotApprovedEnds6WithAStderrEnvelopeUnderJSON(t *testing.T) {
	for _, format := range []string{utils.OutputFormatJSON, utils.OutputFormatTable} {
		t.Run(format, func(t *testing.T) {
			helper := exec.Command(os.Args[0], "-test.run=^TestExitNotApprovedHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_NOT_APPROVED_HELPER="+format)
			var stdout, stderr bytes.Buffer
			helper.Stdout, helper.Stderr = &stdout, &stderr

			err := helper.Run()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, utils.ExitCodeNotApproved, exitErr.ExitCode())
			assert.Equal(t, `{"event":1}`+"\n", stdout.String())
			if format == utils.OutputFormatTable {
				assert.Empty(t, stderr.String())
				return
			}
			var envelope struct {
				ExitCode  int    `json:"exit_code"`
				ErrorCode string `json:"error_code"`
				Command   string `json:"command"`
			}
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
			assert.Equal(t, utils.ExitCodeNotApproved, envelope.ExitCode)
			assert.Equal(t, utils.ErrorCodeNotApproved, envelope.ErrorCode)
			assert.Equal(t, 1, bytes.Count(stderr.Bytes(), []byte("\n")))
		})
	}
}

func TestExitNotApprovedHelperProcess(t *testing.T) {
	format := os.Getenv("GO_WANT_NOT_APPROVED_HELPER")
	if format == "" {
		return
	}
	utils.OutputFormat = format
	_, _ = os.Stdout.WriteString(`{"event":1}` + "\n")
	exitNotApproved("work_session")
}
