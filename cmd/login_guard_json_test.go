package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const loggedOutHelperMarker = "logged-out-helper"

func TestLoginGuardsNameNotLoggedInUnderJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		badFile  bool
		wantCode string
	}{
		{name: "whoami", args: []string{"whoami"}, wantCode: utils.ErrorCodeNotLoggedIn},
		{name: "workspace ls", args: []string{"workspace", "ls"}, wantCode: utils.ErrorCodeNotLoggedIn},
		{name: "user invite", args: []string{"user", "invite"}, wantCode: utils.ErrorCodeNotLoggedIn},
		{name: "malformed config stays cli_error", args: []string{"whoami"}, badFile: true, wantCode: utils.ErrorCodeCLI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			if tt.badFile {
				dir := filepath.Join(home, ".alpacon")
				require.NoError(t, os.MkdirAll(dir, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o600))
			}

			stdout, stderr, exitCode := runHelperProcess(t, "TestLoggedOutHelperProcess", loggedOutHelperMarker,
				append([]string{"--output", "json"}, tt.args...), "GO_WANT_LOGGED_OUT_HELPER=1", homeEnvVar()+"="+home)

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "stderr: %s", stderr)
			assert.Empty(t, stdout)
			lines := strings.Split(strings.TrimSpace(stderr), "\n")
			var envelope struct {
				ExitCode  int    `json:"exit_code"`
				ErrorCode string `json:"error_code"`
			}
			require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &envelope), "stderr: %s", stderr)
			assert.Equal(t, tt.wantCode, envelope.ErrorCode)
			assert.Equal(t, utils.ExitCodeGeneralError, envelope.ExitCode)
		})
	}
}

// Serial: the child reaches an os.Exit.
func TestLoggedOutHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LOGGED_OUT_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, loggedOutHelperMarker)
	if !ok {
		os.Exit(99)
	}
	os.Args = append([]string{"alpacon"}, args...)
	Execute()
	os.Exit(0)
}
