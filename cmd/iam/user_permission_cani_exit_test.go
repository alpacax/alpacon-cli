package iam

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

func TestExitQuietDeniedEnvelopeOnlyUnderJSON(t *testing.T) {
	for _, format := range []string{utils.OutputFormatJSON, utils.OutputFormatTable} {
		t.Run(format, func(t *testing.T) {
			helper := exec.Command(os.Args[0], "-test.run=^TestExitQuietDeniedHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_QUIET_DENIED_HELPER="+format)
			var stdout, stderr bytes.Buffer
			helper.Stdout, helper.Stderr = &stdout, &stderr

			err := helper.Run()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, utils.ExitCodeGeneralError, exitErr.ExitCode())
			assert.Empty(t, stdout.String())
			if format == utils.OutputFormatTable {
				assert.Empty(t, stderr.String())
				return
			}
			var envelope struct {
				ExitCode  int    `json:"exit_code"`
				ErrorCode string `json:"error_code"`
			}
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
			assert.Equal(t, utils.ExitCodeGeneralError, envelope.ExitCode)
			assert.Equal(t, "permission_not_held", envelope.ErrorCode)
			assert.Equal(t, 1, bytes.Count(stderr.Bytes(), []byte("\n")))
		})
	}
}

func TestExitQuietDeniedHelperProcess(t *testing.T) {
	format := os.Getenv("GO_WANT_QUIET_DENIED_HELPER")
	if format == "" {
		return
	}
	utils.OutputFormat = format
	exitQuietDenied("server:update")
}
