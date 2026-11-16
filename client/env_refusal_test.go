package client

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentRefusalMessage(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"command_dangerous_env", "command_invalid_env"} {
		for _, topLevel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/top_level_%t", code, topLevel), func(t *testing.T) {
				t.Parallel()
				message := "The command environment policy refuses `PATH`, `LD_PRELOAD`: these variables can change which program runs. Remove them, or set them inside the command (`env NAME=value cmd`)."
				body := fmt.Sprintf(`{"code":%q,"field_errors":{"non_field_errors":[{"code":%q,"message":%q}]}}`, code, code, message)
				if topLevel {
					message = "The command environment policy refuses `PATH`: remove it."
					body = fmt.Sprintf(`{"code":%q,"message":%q,"field_errors":{"non_field_errors":[{"code":%q,"message":"Invalid input."}],"env":[{"message":"must not expose this field"}]}}`, code, message, code)
				}
				err := parseAPIError([]byte(body), http.StatusBadRequest)
				require.EqualError(t, err, message)
				assert.Equal(t, code, utils.StructuredErrorCode(err))
			})
		}
	}
}

func TestCodedClientErrorsOmitBugReport(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		footer bool
	}{
		{"dangerous environment", 400, `{"code":"command_dangerous_env","field_errors":{"non_field_errors":[{"code":"command_dangerous_env","message":"Remove PATH."}]}}`, false},
		{"invalid environment", 400, `{"code":"command_invalid_env","field_errors":{"non_field_errors":[{"code":"command_invalid_env","message":"Remove PATH."}]}}`, false},
		{"permission refusal", 403, `{"code":"permission_denied"}`, false},
		{"server failure", 500, `{"code":"command_dangerous_env"}`, true},
		{"unparseable response", 400, `<html>Bad request</html>`, true},
		{"uncoded response", 400, `{"detail":"Bad request"}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := withStatus(parseAPIError([]byte(tc.body), tc.status), tc.status)
			_, stderr := testutil.CaptureOutput(t, func() { utils.CliError("failed: %s", fmt.Errorf("request: %w", err)) })
			if tc.footer {
				assert.Contains(t, stderr, "For issues,")
			} else {
				assert.NotContains(t, stderr, "For issues,")
			}
		})
	}
}
