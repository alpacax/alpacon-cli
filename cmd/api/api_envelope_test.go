package apicmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPICommand_JSONModeEndsStderrWithEnvelopeOnNon2xx(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		contentTyp string
		body       string
		retryAfter string
		wantFirst  string
		wantLine   string
		wantStdout string
		wantRaw    string
	}{
		{
			name: "json body with code", status: http.StatusNotFound, contentTyp: "application/json",
			body:       `{"code":"resource_not_found","detail":"missing"}`,
			wantFirst:  "HTTP 404",
			wantLine:   `{"ok":false,"exit_code":1,"error_code":"resource_not_found","message":"HTTP 404","command":"api","context":{"operation":"api"},"http_status":404,"server":{"detail":"missing"}}`,
			wantStdout: `{"code":"resource_not_found","detail":"missing"}`,
		},
		{
			name: "integer beyond float64 precision", status: http.StatusConflict, contentTyp: "application/json",
			body:       `{"code":"x","sequence":9007199254740993}`,
			wantFirst:  "HTTP 409",
			wantLine:   `{"ok":false,"exit_code":1,"error_code":"x","message":"HTTP 409","command":"api","context":{"operation":"api"},"http_status":409,"server":{"sequence":9007199254740993}}`,
			wantStdout: `{"code":"x","sequence":9007199254740993}`,
			wantRaw:    `"sequence":9007199254740993`,
		},
		{
			name: "html body from a proxy", status: http.StatusBadGateway, contentTyp: "text/html",
			body: "<html>bad gateway</html>", retryAfter: "7",
			wantFirst:  "HTTP 502",
			wantLine:   `{"ok":false,"exit_code":1,"error_code":"http_502","message":"HTTP 502","command":"api","context":{"operation":"api"},"http_status":502,"retry_after":7}`,
			wantStdout: "<html>bad gateway</html>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previousClient, previousExit, previousFormat, previousName := newAPIClient, exitAPI, utils.OutputFormat, utils.CommandName
			t.Cleanup(func() {
				newAPIClient, exitAPI, utils.OutputFormat, utils.CommandName = previousClient, previousExit, previousFormat, previousName
			})
			newAPIClient = func() (*client.AlpaconClient, error) {
				return apiTestClient(func(r *http.Request) (*http.Response, error) {
					response := apiTestResponse(tc.status, tc.contentTyp, tc.body)
					if tc.retryAfter != "" {
						response.Header.Set("Retry-After", tc.retryAfter)
					}
					return response, nil
				}), nil
			}
			exitCode := 0
			exitAPI = func(code int) { exitCode = code }
			utils.OutputFormat, utils.CommandName = utils.OutputFormatJSON, "api"
			command := newCommand()
			command.SetArgs([]string{"/x"})
			var executeErr error

			stdout, stderr := testutil.CaptureOutput(t, func() { executeErr = command.Execute() })

			require.NoError(t, executeErr)
			assert.Equal(t, 1, exitCode)
			assert.Equal(t, tc.wantStdout, strings.TrimSpace(stdout))
			lines := strings.Split(strings.TrimSpace(stderr), "\n")
			assert.Equal(t, tc.wantFirst, lines[0])
			assert.JSONEq(t, tc.wantLine, lines[len(lines)-1])
			assert.Contains(t, lines[len(lines)-1], tc.wantRaw)
		})
	}
}

// Serial: the child reaches an os.Exit, and the parent's own run returns at once.
func TestAPICommand_RedirectLoopUnderJSONKeepsTheStatus(t *testing.T) {
	if os.Getenv("ALPACON_API_ENVELOPE_CHILD") == "loop" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			return apiTestClient(func(*http.Request) (*http.Response, error) {
				response := apiTestResponse(http.StatusFound, "text/plain", "")
				response.Header.Set("Location", "/x")
				return response, nil
			}), nil
		}
		utils.OutputFormat, utils.CommandName = utils.OutputFormatJSON, "api"
		command := newCommand()
		command.SetArgs([]string{"/x"})
		_ = command.Execute()
		os.Exit(5)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_RedirectLoopUnderJSONKeepsTheStatus$")
	child.Env = append(os.Environ(), "ALPACON_API_ENVELOPE_CHILD=loop")
	var stderr bytes.Buffer
	child.Stderr = &stderr

	err := child.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &envelope), "stderr: %s", stderr.String())
	assert.Equal(t, "http_302", envelope["error_code"])
	assert.InDelta(t, http.StatusFound, envelope["http_status"], 0)
}

// Serial: the child reaches an os.Exit, and the parent's own run returns at once.
func TestAPICommand_MissingEndpointUnderJSONIsUsageEnvelope(t *testing.T) {
	if os.Getenv("ALPACON_API_ENVELOPE_CHILD") == "1" {
		utils.OutputFormat, utils.CommandName = utils.OutputFormatJSON, "api"
		command := newCommand()
		command.SetArgs(nil)
		_ = command.Execute()
		os.Exit(5)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_MissingEndpointUnderJSONIsUsageEnvelope$")
	child.Env = append(os.Environ(), "ALPACON_API_ENVELOPE_CHILD=1")
	var stderr bytes.Buffer
	child.Stderr = &stderr

	err := child.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.JSONEq(t, `{"ok":false,"exit_code":2,"error_code":"usage_error","message":"expected one ENDPOINT","command":"api","context":{"operation":"api"}}`, strings.TrimSpace(stderr.String()))
}
