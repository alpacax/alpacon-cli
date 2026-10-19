package websh

import (
	"bytes"
	"encoding/json"
	"os"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebshParseFailureUnderJSONIsOneEnvelope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args string
	}{
		{"read-only with a bad value", "--read-only=maybe,srv"},
		{"output without value", "--output"},
		{"missing server", "-s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			helper := osexec.Command(os.Args[0], "-test.run=^TestWebshParseEnvelopeHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_WEBSH_PARSE_ENVELOPE_HELPER="+tt.args)
			var stdout, stderr bytes.Buffer
			helper.Stdout, helper.Stderr = &stdout, &stderr

			err := helper.Run()

			var exitErr *osexec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, 1, exitErr.ExitCode())
			assert.Empty(t, stdout.String())
			lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
			require.Len(t, lines, 1)
			var env map[string]any
			require.NoError(t, json.Unmarshal([]byte(lines[0]), &env))
			assert.Equal(t, false, env["ok"])
			assert.NotEmpty(t, env["error_code"])
			assert.InDelta(t, 1, env["exit_code"], 0)
		})
	}
}

func TestWebshParseEnvelopeHelperProcess(t *testing.T) {
	raw := os.Getenv("GO_WANT_WEBSH_PARSE_ENVELOPE_HELPER")
	if raw == "" {
		return
	}
	utils.OutputFormat = utils.OutputFormatJSON
	WebshCmd.Run(WebshCmd, strings.Split(raw, ","))
	os.Exit(0)
}
