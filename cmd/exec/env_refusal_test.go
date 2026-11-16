package exec

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestExecEnvironmentRefusalOutput(t *testing.T) {
	for _, tc := range []struct {
		code     string
		sentence string
	}{
		{"command_dangerous_env", "The server refused environment variables that can change which program runs. Remove them, or set them inside the command (env NAME=value cmd)."},
		{"command_invalid_env", "The server refused the command's environment: names must use letters, digits and underscores, values must be strings of at most 4096 characters, and at most 64 variables are allowed."},
	} {
		code := tc.code
		for _, server := range []struct {
			name    string
			message string
		}{
			{"empty", ""},
			{"generic", "Invalid input."},
			{"specific", "The command environment policy refuses `PATH`, `LD_PRELOAD`: these variables can change which program runs. Remove them, or set them inside the command (`env NAME=value cmd`)."},
		} {
			serverMessage := server.message
			for _, topLevel := range []bool{false, true} {
				for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
					t.Run(fmt.Sprintf("%s/%s/top_level_%t/message_%s", code, format, topLevel, server.name), func(t *testing.T) {
						message := serverMessage
						if message == "" || message == "Invalid input." {
							message = tc.sentence
						}
						ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Content-Type", "application/json")
							switch {
							case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
								_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"550e8400-e29b-41d4-a716-446655440000","name":"my-server"}]}`))
							case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
								w.WriteHeader(http.StatusBadRequest)
								if topLevel {
									_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q,"field_errors":{"non_field_errors":[{"code":%q,"message":"Invalid input."}],"env":[{"message":"ENV_VALUE_MUST_NOT_APPEAR"}]},"env":{"PATH":"ENV_VALUE_MUST_NOT_APPEAR"}}`, code, serverMessage, code)
									return
								}
								_, _ = fmt.Fprintf(w, `{"code":%q,"field_errors":{"non_field_errors":[{"code":%q,"message":%q}]}}`, code, code, serverMessage)
							default:
								http.NotFound(w, r)
							}
						}))
						defer ts.Close()
						_, stderr, exitCode := runExecHelper(t, ts.URL, "--output", format, "my-server", "--", "uptime")
						assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "stderr: %s", stderr)
						assert.Contains(t, stderr, message)
						assert.NotContains(t, stderr, "For issues,")
						assert.NotContains(t, stderr, "Invalid input.")
						assert.NotContains(t, stderr, "ENV_VALUE_MUST_NOT_APPEAR")
						if format == utils.OutputFormatJSON {
							envelope := lastEnvelope(t, stderr)
							assert.Equal(t, code, envelope.ErrorCode)
							assert.Contains(t, envelope.Message, message)
						}
					})
				}
			}
		}
	}
}
