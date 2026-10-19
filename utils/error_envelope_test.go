package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCliErrorEnvelope(t *testing.T) {
	t.Parallel()
	env := buildCliErrorEnvelope("extend", "work_session_not_usable", "Failed to extend work session: refused.")

	assert.False(t, env.OK)
	assert.Equal(t, 1, env.ExitCode)
	assert.Equal(t, "work_session_not_usable", env.ErrorCode)
	assert.Equal(t, "Failed to extend work session: refused.", env.Message)
	assert.Equal(t, "extend", env.Context.Operation)
	assert.Empty(t, env.NextActions)
}

func TestBuildCliErrorEnvelope_NoCodeFallsBackToCLIError(t *testing.T) {
	t.Parallel()
	env := buildCliErrorEnvelope("use", "", "boom")

	assert.Equal(t, ErrorCodeCLI, env.ErrorCode)
	assert.Equal(t, "use", env.Context.Operation)
}

func TestBuildCliErrorEnvelopeFromErr_ExtractsServerCode(t *testing.T) {
	t.Parallel()
	// ParseErrorResponse falls back to parsing the legacy "msg; code: X; source: Y" string form.
	err := errors.New("WorkSession is not usable; code: work_session_not_usable; source: work_session")
	env := buildCliErrorEnvelopeFromErr("extend", err, "Failed to extend work session: refused.")

	assert.Equal(t, "work_session_not_usable", env.ErrorCode)
	assert.Equal(t, "extend", env.Context.Operation)
}

func TestBuildCliErrorEnvelopeFromErr_NilErr(t *testing.T) {
	t.Parallel()
	env := buildCliErrorEnvelopeFromErr("recording", nil, "No recordings found for session ses-1.")

	assert.Equal(t, ErrorCodeCLI, env.ErrorCode)
	assert.Equal(t, "No recordings found for session ses-1.", env.Message)
}

func TestBuildCliErrorEnvelopeFromErr_PlainErr(t *testing.T) {
	t.Parallel()
	env := buildCliErrorEnvelopeFromErr("use", errors.New("config write failed"), "config write failed")

	assert.Equal(t, ErrorCodeCLI, env.ErrorCode)
}

// The RBAC role gate and the token-scope gate now answer a 403 with a coded
// body instead of a legacy bare {"detail": ...}—--output json must still carry
// error_code for these two new families the same way it always has for any
// other coded refusal.
func TestBuildCliErrorEnvelopeFromErr_ExtractsRBACPermissionRequiredCode(t *testing.T) {
	t.Parallel()
	err := errors.New(`request failed with status 403: {"code": "rbac_permission_required", "gate": "role", "missing": "role"}`)
	env := buildCliErrorEnvelopeFromErr("role grant", err, "Failed to grant role: you do not have permission to make that change.")

	assert.Equal(t, "rbac_permission_required", env.ErrorCode)
	assert.Equal(t, "role grant", env.Context.Operation)
}

func TestBuildCliErrorEnvelopeFromErr_ExtractsTokenScopeMissingCode(t *testing.T) {
	t.Parallel()
	err := errors.New(`request failed with status 403: {"code": "api_token_scope_missing", "gate": "token_scope", "missing": ["scope:server:create"]}`)
	env := buildCliErrorEnvelopeFromErr("server create", err, "Failed to create server: this API token is missing the required scope(s): scope:server:create.")

	assert.Equal(t, "api_token_scope_missing", env.ErrorCode)
}

func TestUsageErrorCodeConstant(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "usage_error", UsageErrorCode)
}

func TestBuildCliUsageErrorEnvelope_CarriesUsageCodeAndExitTwo(t *testing.T) {
	t.Parallel()
	env := buildCliUsageErrorEnvelope("extend", "Either --expires-in or --expires-at is required.")

	assert.False(t, env.OK)
	assert.Equal(t, ExitCodeUsageError, env.ExitCode)
	assert.Equal(t, UsageErrorCode, env.ErrorCode)
	assert.Equal(t, "Either --expires-in or --expires-at is required.", env.Message)
	assert.Equal(t, "extend", env.Context.Operation)
}

func TestBuildCliErrorEnvelope_DefaultsExitCodeToOne(t *testing.T) {
	t.Parallel()
	// Pins the default the new helper has to override; without the override the
	// envelope would claim 1 while the process exits 6.
	envelope := buildCliErrorEnvelope("work-session create", "", "work session was rejected")

	assert.Equal(t, 1, envelope.ExitCode)
}

func TestCliErrorEnvelopeWithExit_InvalidIDUsesUsageExit(t *testing.T) {
	for _, format := range []string{OutputFormatJSON, OutputFormatTable} {
		t.Run(format, func(t *testing.T) {
			help := exec.Command(os.Args[0], "-test.run=^TestCliErrorEnvelopeWithExitInvalidIDHelperProcess$")
			help.Env = append(os.Environ(), "GO_WANT_INVALID_ID_ENVELOPE_HELPER="+format)
			var stderr bytes.Buffer
			help.Stderr = &stderr

			err := help.Run()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, ExitCodeUsageError, exitErr.ExitCode())
			if format == OutputFormatTable {
				assert.Contains(t, stderr.String(), "Failed to wait: invalid target ID: invalid id.")
				return
			}
			var envelope JSONErrorEnvelope[cliErrorCtx]
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
			assert.Equal(t, ExitCodeUsageError, envelope.ExitCode)
			assert.Equal(t, UsageErrorCode, envelope.ErrorCode)
			assert.Equal(t, "wait", envelope.Context.Operation)
		})
	}
}

func TestCliErrorEnvelopeWithExitInvalidIDHelperProcess(t *testing.T) {
	format := os.Getenv("GO_WANT_INVALID_ID_ENVELOPE_HELPER")
	if format == "" {
		return
	}
	OutputFormat = format
	err := fmt.Errorf("invalid target ID: %w", ErrInvalidID)
	CliErrorEnvelopeWithExit("wait", err, "Failed to wait: %s.", err)
}

func TestBuildCliErrorEnvelopeFromErr_SudoBypassAgentSessionRefusals(t *testing.T) {
	t.Parallel()
	for _, code := range []string{SudoPolicyBypassAgentSessionNeedsPresence, SudoPolicyBypassAgentSessionNeedsApproval} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			err := errors.New("code: " + code + "; source: ")
			env := buildCliErrorEnvelopeFromErr("create", err, "Failed to create work session: next step.")
			assert.False(t, env.OK)
			assert.Equal(t, code, env.ErrorCode)
			assert.Equal(t, "create", env.Context.Operation)
			rendered, jerr := FormatJSON(env)
			require.NoError(t, jerr)
			assert.Contains(t, rendered, `"error_code": "`+code+`"`)
		})
	}
}

func TestNewFailureEnvelope(t *testing.T) {
	t.Run("explicit code and operation", func(t *testing.T) {
		withCommandName(t, "workspace ls")
		env := newFailureEnvelope(7, ErrorCodeNotLoggedIn, "login", nil, "Not logged in.")

		assert.Equal(t, 7, env.ExitCode)
		assert.Equal(t, ErrorCodeNotLoggedIn, env.ErrorCode)
		assert.Equal(t, "Not logged in.", env.Message)
		assert.Equal(t, "login", env.Context.Operation)
		assert.Equal(t, "workspace ls", env.Command)
		assert.False(t, env.OK)
	})

	t.Run("operation defaults to the command name", func(t *testing.T) {
		withCommandName(t, "server ls")
		env := newFailureEnvelope(1, "", "", nil, "x")

		assert.Equal(t, "server ls", env.Context.Operation)
	})

	t.Run("carries status retry after and server fields", func(t *testing.T) {
		err := fmt.Errorf("w: %w", &fakeAPIError{
			code: "throttled", status: 429, retry: 1500 * time.Millisecond,
			fields: map[string]any{"gate": "plan"},
		})
		env := newFailureEnvelope(1, "", "list", err, "slow down")

		assert.Equal(t, "throttled", env.ErrorCode)
		assert.Equal(t, 429, env.HTTPStatus)
		assert.Equal(t, 2, env.RetryAfter)
		assert.Equal(t, map[string]any{"gate": "plan"}, env.Server)
	})

	t.Run("no server data leaves the optional fields empty", func(t *testing.T) {
		env := newFailureEnvelope(1, "", "list", errors.New("x"), "x")

		assert.Equal(t, ErrorCodeCLI, env.ErrorCode)
		assert.Zero(t, env.HTTPStatus)
		assert.Zero(t, env.RetryAfter)
		assert.Nil(t, env.Server)
	})
}

func TestCliErrorHelpersExitInSubprocess(t *testing.T) {
	apiErr := &fakeAPIError{code: "rbac_permission_required", status: 403, fields: map[string]any{"gate": "role"}}
	_ = apiErr
	tests := []struct {
		name     string
		mode     string
		format   string
		wantExit int
		check    func(t *testing.T, stderr string)
	}{
		{
			name: "exit json envelope without footer", mode: "exit", format: OutputFormatJSON, wantExit: 1,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, 1, env.ExitCode)
				assert.Equal(t, ErrorCodeCLI, env.ErrorCode)
				assert.Equal(t, "Failed: boom.", env.Message)
				assert.Equal(t, "greet world", env.Command)
				assert.NotContains(t, stderr, "For issues")
				assert.NotContains(t, stderr, "\x1b")
			},
		},
		{
			name: "exit table keeps the footer", mode: "exit", format: OutputFormatTable, wantExit: 1,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "Error: Failed: boom.")
				assert.Contains(t, stderr, "For issues")
			},
		},
		{
			name: "exit json uses the error argument", mode: "exit-err", format: OutputFormatJSON, wantExit: 1,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, "http_404", env.ErrorCode)
				assert.Equal(t, 404, env.HTTPStatus)
			},
		},
		{
			name: "exit code json", mode: "exit-code", format: OutputFormatJSON, wantExit: 6,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, 6, env.ExitCode)
				assert.Equal(t, ErrorCodeCLI, env.ErrorCode)
			},
		},
		{
			name: "exit code table", mode: "exit-code", format: OutputFormatTable, wantExit: 6,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "Error: refused")
				assert.NotContains(t, stderr, "For issues")
			},
		},
		{
			name: "with code json", mode: "with-code", format: OutputFormatJSON, wantExit: 1,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, ErrorCodeNotLoggedIn, env.ErrorCode)
				assert.Equal(t, "Not logged in.", env.Message)
			},
		},
		{
			name: "with code table", mode: "with-code", format: OutputFormatTable, wantExit: 1,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "Error: Not logged in.")
				assert.Contains(t, stderr, "For issues", "exit 1 keeps CliErrorWithExit's footer")
				assert.NotContains(t, stderr, "not_logged_in")
			},
		},
		{
			name: "with code table non-1 omits footer", mode: "with-code-6", format: OutputFormatTable, wantExit: 6,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "Error: refused")
				assert.NotContains(t, stderr, "For issues")
			},
		},
		{
			name: "envelope helper keeps code and exit", mode: "envelope-code", format: OutputFormatJSON, wantExit: 6,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, 6, env.ExitCode)
				assert.Equal(t, "rbac_permission_required", env.ErrorCode)
				assert.Equal(t, 403, env.HTTPStatus)
				assert.Equal(t, map[string]any{"gate": "role"}, env.Server)
				assert.Equal(t, "greet world", env.Command)
			},
		},
		{
			name: "positive int json is a usage error", mode: "positive-int", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, UsageErrorCode, env.ErrorCode)
				assert.Equal(t, ExitCodeUsageError, env.ExitCode)
				assert.Equal(t, "--timeout must be a positive integer.", env.Message)
			},
		},
		{
			name: "exit code 2 json is a usage error", mode: "exit-code-usage", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Equal(t, UsageErrorCode, decodeSingleLineEnvelope(t, stderr).ErrorCode)
			},
		},
		{
			name: "usage helper", mode: "usage", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, UsageErrorCode, env.ErrorCode)
				assert.Equal(t, ExitCodeUsageError, env.ExitCode)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			help := exec.Command(os.Args[0], "-test.run=^TestCliErrorHelpersHelperProcess$")
			help.Env = append(os.Environ(), "GO_WANT_CLI_HELPERS="+tt.mode+":"+tt.format)
			var stderr bytes.Buffer
			help.Stderr = &stderr

			err := help.Run()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, tt.wantExit, exitErr.ExitCode())
			tt.check(t, stderr.String())
		})
	}
}

// Serial: the child reaches an os.Exit, and the parent's own run returns at once.
func TestCliErrorHelpersHelperProcess(t *testing.T) {
	spec := os.Getenv("GO_WANT_CLI_HELPERS")
	if spec == "" {
		return
	}
	mode, format, _ := strings.Cut(spec, ":")
	OutputFormat = format
	CommandName = "greet world"
	apiErr := &fakeAPIError{code: "rbac_permission_required", status: 403, fields: map[string]any{"gate": "role"}}
	switch mode {
	case "exit":
		CliErrorWithExit("Failed: %s.", "boom")
	case "exit-err":
		CliErrorWithExit("Failed: %s.", &fakeAPIError{status: 404})
	case "exit-code":
		CliErrorWithExitCode(6, "refused")
	case "positive-int":
		RequirePositiveInt("timeout", 0)
	case "exit-code-usage":
		CliErrorWithExitCode(ExitCodeUsageError, "bad")
	case "with-code":
		CliErrorWithCode(1, ErrorCodeNotLoggedIn, "Not logged in.")
	case "with-code-6":
		CliErrorWithCode(6, ErrorCodeNotApproved, "refused")
	case "envelope-code":
		CliErrorEnvelopeWithExitCode(6, "", apiErr, "Failed: %s.", apiErr)
	case "usage":
		CliUsageErrorEnvelopeWithExit("", "bad flag")
	}
}

func withCommandName(t *testing.T, name string) {
	t.Helper()
	original := CommandName
	CommandName = name
	t.Cleanup(func() { CommandName = original })
}

func decodeSingleLineEnvelope(t *testing.T, stderr string) JSONErrorEnvelope[cliErrorCtx] {
	t.Helper()
	trimmed := strings.TrimSuffix(stderr, "\n")
	require.NotContains(t, trimmed, "\n", "envelope must be one line: %q", stderr)
	var env JSONErrorEnvelope[cliErrorCtx]
	require.NoError(t, json.Unmarshal([]byte(trimmed), &env), "stderr: %s", stderr)
	return env
}
