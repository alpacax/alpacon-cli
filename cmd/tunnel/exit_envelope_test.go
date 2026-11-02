package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitTunnelEndsWithAnEnvelopeUnderJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		helper   string
		wantExit int
		wantCode string // empty: table mode, no envelope
	}{
		{name: "child exit json", helper: "child:json", wantExit: 3, wantCode: utils.ErrorCodeChildCommandFailed},
		{name: "tunnel failure json", helper: "error:json", wantExit: 1, wantCode: utils.ErrorCodeCLI},
		{name: "child exit table", helper: "child:table", wantExit: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			helper := osexec.Command(os.Args[0], "-test.run=^TestExitTunnelHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_EXIT_TUNNEL_HELPER="+tt.helper)
			var stdout, stderr bytes.Buffer
			helper.Stdout, helper.Stderr = &stdout, &stderr

			err := helper.Run()

			var exitErr *osexec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, tt.wantExit, exitErr.ExitCode())
			assert.Empty(t, stdout.String())
			if tt.wantCode == "" {
				assert.Empty(t, stderr.String())
				return
			}
			var env utils.JSONErrorEnvelope[map[string]any]
			require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(stderr.String(), "\n")), &env), "stderr: %q", stderr.String())
			assert.Equal(t, tt.wantExit, env.ExitCode)
			assert.Equal(t, tt.wantCode, env.ErrorCode)
		})
	}
}

// Serial: the child reaches an os.Exit.
func TestExitTunnelHelperProcess(t *testing.T) {
	mode, format, ok := strings.Cut(os.Getenv("GO_WANT_EXIT_TUNNEL_HELPER"), ":")
	if !ok {
		return
	}
	utils.OutputFormat = format
	if mode == "error" {
		exitTunnel(0, errors.New("failed to establish tunnel connection"))
	}
	exitTunnel(3, nil)
}
