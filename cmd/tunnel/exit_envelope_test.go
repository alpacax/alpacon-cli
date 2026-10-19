package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: the child reaches an os.Exit.
func TestExitTunnelEndsWithAnEnvelopeUnderJSON(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		wantExit   int
		wantCode   string
		wantRemote *int
	}{
		{"child command failed", "child", 42, utils.ErrorCodeChildCommandFailed, new(42)},
		{"error without a child code", "err", 1, utils.ErrorCodeCLI, nil},
		{"error with a child code", "err-code", 5, utils.ErrorCodeCLI, nil},
	}
	for _, tt := range tests {
		t.Run(strings.ReplaceAll(tt.name, " ", "_"), func(t *testing.T) {
			helper := exec.Command(os.Args[0], "-test.run=^TestExitTunnelHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_EXIT_TUNNEL_HELPER="+tt.mode)
			var stdout, stderr bytes.Buffer
			helper.Stdout, helper.Stderr = &stdout, &stderr

			err := helper.Run()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, tt.wantExit, exitErr.ExitCode())
			assert.Empty(t, stdout.String())
			lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
			assert.Len(t, lines, 1)
			var env struct {
				ExitCode       int    `json:"exit_code"`
				ErrorCode      string `json:"error_code"`
				RemoteExitCode *int   `json:"remote_exit_code"`
			}
			require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &env), "stderr: %s", stderr.String())
			assert.Equal(t, tt.wantExit, env.ExitCode)
			assert.Equal(t, tt.wantCode, env.ErrorCode)
			assert.Equal(t, tt.wantRemote, env.RemoteExitCode)
		})
	}
}

func TestExitTunnelTableModeKeepsTheErrorLine(t *testing.T) {
	helper := exec.Command(os.Args[0], "-test.run=^TestExitTunnelHelperProcess$")
	helper.Env = append(os.Environ(), "GO_WANT_EXIT_TUNNEL_HELPER=err", "GO_WANT_EXIT_TUNNEL_TABLE=1")
	var stderr bytes.Buffer
	helper.Stderr = &stderr

	err := helper.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Contains(t, stderr.String(), "Error: tunnel broke")
	assert.NotContains(t, stderr.String(), "error_code")
}

func TestExitTunnelHelperProcess(t *testing.T) {
	mode := os.Getenv("GO_WANT_EXIT_TUNNEL_HELPER")
	if mode == "" {
		return
	}
	utils.OutputFormat = utils.OutputFormatJSON
	if os.Getenv("GO_WANT_EXIT_TUNNEL_TABLE") == "1" {
		utils.OutputFormat = utils.OutputFormatTable
	}
	switch mode {
	case "child":
		exitTunnel(42, nil)
	case "err":
		exitTunnel(0, errors.New("tunnel broke"))
	case "err-code":
		exitTunnel(5, errors.New("tunnel broke"))
	}
	os.Exit(0)
}
