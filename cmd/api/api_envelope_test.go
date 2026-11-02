package apicmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type apiEnvelope struct {
	ExitCode   int            `json:"exit_code"`
	ErrorCode  string         `json:"error_code"`
	Message    string         `json:"message"`
	HTTPStatus int            `json:"http_status"`
	RetryAfter int            `json:"retry_after"`
	Server     map[string]any `json:"server"`
}

func TestAPINon2xxEndsWithAnEnvelopeUnderJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		helper     string
		wantStdout string
		want       apiEnvelope
	}{
		{
			name:       "coded refusal",
			helper:     "coded",
			wantStdout: `"code":"rbac_permission_required"`,
			want:       apiEnvelope{ExitCode: 1, ErrorCode: "rbac_permission_required", Message: "HTTP 403", HTTPStatus: 403, Server: map[string]any{"detail": "no"}},
		},
		{
			name:       "proxy outage",
			helper:     "outage",
			wantStdout: "<html>",
			want:       apiEnvelope{ExitCode: 1, ErrorCode: "http_503", Message: "HTTP 503", HTTPStatus: 503, RetryAfter: 7},
		},
		{
			name:   "not found",
			helper: "missing",
			want:   apiEnvelope{ExitCode: 1, ErrorCode: "http_404", Message: "HTTP 404", HTTPStatus: 404},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			helper := osexec.Command(os.Args[0], "-test.run=^TestAPIEnvelopeHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_API_ENVELOPE_HELPER="+tt.helper)
			var stdout, stderr bytes.Buffer
			helper.Stdout, helper.Stderr = &stdout, &stderr

			err := helper.Run()

			var exitErr *osexec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, utils.ExitCodeGeneralError, exitErr.ExitCode())
			assert.Contains(t, stdout.String(), tt.wantStdout)
			var got apiEnvelope
			require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(stderr.String(), "\n")), &got), "stderr: %q", stderr.String())
			assert.Equal(t, tt.want, got)
		})
	}
}

// Serial: the child reaches an os.Exit.
func TestAPIEnvelopeHelperProcess(t *testing.T) {
	mode := os.Getenv("GO_WANT_API_ENVELOPE_HELPER")
	if mode == "" {
		return
	}
	responses := map[string]*http.Response{
		"coded":   apiTestResponse(http.StatusForbidden, "application/json", `{"code":"rbac_permission_required","detail":"no"}`),
		"outage":  apiTestResponse(http.StatusServiceUnavailable, "text/html", "<html>down</html>"),
		"missing": apiTestResponse(http.StatusNotFound, "application/json", `{}`),
	}
	if mode == "outage" {
		responses[mode].Header.Set("Retry-After", "7")
	}
	newAPIClient = func() (*client.AlpaconClient, error) {
		return apiTestClient(func(*http.Request) (*http.Response, error) { return responses[mode], nil }), nil
	}
	utils.OutputFormat = utils.OutputFormatJSON
	command := newCommand()
	command.SetArgs([]string{"/api/servers/servers/"})
	_ = command.Execute()
	os.Exit(0)
}
