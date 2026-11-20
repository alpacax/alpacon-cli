package exec

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecAppliesItsOwnOutputFlagToTheInjectedStreams(t *testing.T) {
	t.Parallel()
	build := func(ios *utils.IOStreams) *cobra.Command { return NewExecCmd(cmdtest.NoClient, ios) }

	_, stderr, code := cmdtest.RunCaptured(build, "--output", "json", "--bogus")

	assert.Equal(t, utils.ExitCodeUsageError, code)
	var envelope struct {
		ErrorCode string `json:"error_code"`
		Message   string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), stderr)
	assert.Equal(t, utils.ErrorCodeUsage, envelope.ErrorCode)
	assert.NotContains(t, envelope.Message, "--help", "json mode carries no table-mode help pointer")
}

// A command built on injected streams, with no harness wiring Cobra's writers to them, prints
// a detached job's confirmation and a submission refusal into those streams in either format.
func TestExecDetachPrintsIntoTheStreamsItWasBuiltWith(t *testing.T) {
	// Serial: it sets HOME and ALPACON_WORK_SESSION.
	accepted := func(w http.ResponseWriter) { _, _ = w.Write([]byte(`[{"id":"cmd-file-1","shell":"file"}]`)) }
	refused := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "file_exec_unsupported_agent", "source": "command"}`))
	}
	tests := []struct {
		name       string
		format     string
		respond    func(http.ResponseWriter)
		wantOut    string
		wantErrOut string
		wantFailed bool
	}{
		{name: "table", format: utils.OutputFormatTable, respond: accepted, wantOut: "Job submitted: cmd-file-1\n",
			wantErrOut: "Run `alpacon exec logs cmd-file-1` to check the result.\n"},
		{name: "json", format: utils.OutputFormatJSON, respond: accepted, wantOut: "{\n  \"job_id\": \"cmd-file-1\"\n}\n"},
		{name: "refused", format: utils.OutputFormatTable, respond: refused, wantFailed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capture fileLaneHelperCapture
			ts := newFileLaneServer(&capture, tt.respond)
			defer ts.Close()
			home := t.TempDir()
			writeExecCommandTestConfig(t, home, ts.URL)
			t.Setenv("HOME", home)
			t.Setenv("ALPACON_WORK_SESSION", "")
			script := filepath.Join(t.TempDir(), "deploy.sh")
			require.NoError(t, os.WriteFile(script, []byte("#!/bin/bash\necho deploy\n"), 0o600))
			ios, out, errOut := utils.TestIOStreams()
			require.NoError(t, ios.SetOutputFormat(tt.format))
			command := NewExecCmd(testClientFactory(ts.URL), ios)

			err := command.RunE(command, []string{"--detach", "--file", "/opt/deploy.sh", "--file-from", script, "root@prod"})

			if !tt.wantFailed {
				require.NoError(t, err)
				assert.Equal(t, tt.wantOut, out.String())
				assert.Equal(t, tt.wantErrOut, errOut.String())
				return
			}
			var exitErr *utils.ExitError
			require.ErrorAs(t, err, &exitErr)
			ios.FprintExitError(errOut, exitErr)
			assert.Empty(t, out.String())
			assert.Contains(t, errOut.String(), "does not offer the file lane")
		})
	}
}
