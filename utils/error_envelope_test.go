package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFailureEnvelopeClassifiesTheCodeFromErr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "legacy code text", err: errors.New("WorkSession is not usable; code: work_session_not_usable; source: work_session"), want: "work_session_not_usable"},
		{name: "rbac json body", err: errors.New(`request failed with status 403: {"code": "rbac_permission_required", "gate": "role", "missing": "role"}`), want: "rbac_permission_required"},
		{name: "token scope json body", err: errors.New(`request failed with status 403: {"code": "api_token_scope_missing", "gate": "token_scope", "missing": ["scope:server:create"]}`), want: "api_token_scope_missing"},
		{name: "sudo bypass needs presence", err: errors.New("code: " + SudoPolicyBypassAgentSessionNeedsPresence + "; source: "), want: SudoPolicyBypassAgentSessionNeedsPresence},
		{name: "sudo bypass needs approval", err: errors.New("code: " + SudoPolicyBypassAgentSessionNeedsApproval + "; source: "), want: SudoPolicyBypassAgentSessionNeedsApproval},
		{name: "nil error", err: nil, want: ErrorCodeCLI},
		{name: "plain error", err: errors.New("config write failed"), want: ErrorCodeCLI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newFailureEnvelope(ExitCodeGeneralError, "", "extend", tt.err, "Failed to extend work session: refused.")

			assert.False(t, env.OK)
			assert.Equal(t, 1, env.ExitCode)
			assert.Equal(t, tt.want, env.ErrorCode)
			assert.Equal(t, "Failed to extend work session: refused.", env.Message)
			assert.Equal(t, "extend", env.Context.Operation)
		})
	}
}

func TestNewFailureEnvelope_CarriesUsageCodeAndExitTwo(t *testing.T) {
	t.Parallel()
	env := newFailureEnvelope(ExitCodeUsageError, ErrorCodeUsage, "extend", nil, "Either --expires-in or --expires-at is required.")

	assert.False(t, env.OK)
	assert.Equal(t, ExitCodeUsageError, env.ExitCode)
	assert.Equal(t, ErrorCodeUsage, env.ErrorCode)
	assert.Equal(t, "Either --expires-in or --expires-at is required.", env.Message)
	assert.Equal(t, "extend", env.Context.Operation)
}

func TestNewFailureEnvelopeFallsBackToTheExitCodesCode(t *testing.T) {
	t.Parallel()
	serverErr := &fakeAPIError{code: "rbac_permission_required", status: 403}
	tests := []struct {
		name     string
		exitCode int
		code     string
		err      error
		want     string
	}{
		{name: "usage exit without a cause", exitCode: ExitCodeUsageError, want: ErrorCodeUsage},
		{name: "not approved exit without a cause", exitCode: ExitCodeNotApproved, want: ErrorCodeNotApproved},
		{name: "not approved exit with a plain cause", exitCode: ExitCodeNotApproved, err: errors.New("x"), want: ErrorCodeNotApproved},
		{name: "server code wins over the usage exit", exitCode: ExitCodeUsageError, err: serverErr, want: "rbac_permission_required"},
		{name: "server code wins over the not approved exit", exitCode: ExitCodeNotApproved, err: serverErr, want: "rbac_permission_required"},
		{name: "http status wins over the exit", exitCode: ExitCodeNotApproved, err: &fakeAPIError{status: 404}, want: "http_404"},
		{name: "busy exit stays cli_error", exitCode: ExitCodeServerBusy, want: ErrorCodeCLI},
		{name: "explicit cli_error survives the not approved exit", exitCode: ExitCodeNotApproved, code: ErrorCodeCLI, want: ErrorCodeCLI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newFailureEnvelope(tt.exitCode, tt.code, "op", tt.err, "m")

			assert.Equal(t, tt.exitCode, env.ExitCode)
			assert.Equal(t, tt.want, env.ErrorCode)
		})
	}
}

func TestRetryAfterSecondsRoundsUp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   time.Duration
		want int
	}{
		{name: "none", in: 0, want: 0},
		{name: "negative", in: -time.Second, want: 0},
		{name: "under a second", in: 200 * time.Millisecond, want: 1},
		{name: "exact seconds", in: 2 * time.Second, want: 2},
		{name: "past a second", in: 1500 * time.Millisecond, want: 2},
		{name: "longest duration does not wrap", in: time.Duration(math.MaxInt64), want: int(math.MaxInt64/int64(time.Second)) + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, retryAfterSeconds(tt.in))
		})
	}
}

func TestCliErrorEnvelopeWithExitOfAUsageErrorExitsTwo(t *testing.T) {
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
			assert.Equal(t, ErrorCodeUsage, envelope.ErrorCode)
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
	err := MarkUsageError(fmt.Errorf("invalid target ID: %w", ErrInvalidID))
	CliErrorEnvelopeWithExit("wait", err, "Failed to wait: %s.", err)
}

func TestNewFailureEnvelopeSessionAuthorizedSudoRefusals(t *testing.T) {
	t.Parallel()
	for _, code := range []string{WorkSessionSudoBypassConsoleOnly, WorkSessionSessionAuthorizedSudoConsoleOnly, SudoPolicyBypassAgentSessionNeedsPresence, SudoPolicySessionAuthorizedAgentSessionNeedsPresence, SudoPolicyBypassAgentSessionNeedsApproval, SudoPolicySessionAuthorizedAgentSessionNeedsApproval} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			err := errors.New("code: " + code + "; source: ")
			env := newFailureEnvelope(ExitCodeGeneralError, "", "create", err, "Failed to create work session: next step.")
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
			name: "exit json uses an error after other arguments", mode: "exit-err", format: OutputFormatJSON, wantExit: 1,
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
				assert.Equal(t, ErrorCodeNotApproved, env.ErrorCode)
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
				assert.Equal(t, ErrorCodeUsage, env.ErrorCode)
				assert.Equal(t, ExitCodeUsageError, env.ExitCode)
				assert.Equal(t, "--timeout must be a positive integer.", env.Message)
			},
		},
		{
			name: "exit code 2 json is a usage error", mode: "exit-code-usage", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Equal(t, ErrorCodeUsage, decodeSingleLineEnvelope(t, stderr).ErrorCode)
			},
		},
		{
			name: "prompt cut short by end of input", mode: "prompt-eof", format: OutputFormatJSON, wantExit: 1,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				head, last, found := strings.Cut(strings.TrimSuffix(stderr, "\n"), "\n")
				require.True(t, found, "stderr: %q", stderr)
				assert.Equal(t, "Username: ", head)
				var env JSONErrorEnvelope[cliErrorCtx]
				require.NoError(t, json.Unmarshal([]byte(last), &env), "stderr: %q", stderr)
				assert.Equal(t, "No input received.", env.Message)
			},
		},
		{
			name: "error helper with a usage error argument exits 2 in table", mode: "exit-usage-arg", format: OutputFormatTable, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "Error: Failed: invalid note ID: invalid id.")
				assert.NotContains(t, stderr, "For issues")
			},
		},
		{
			name: "error helper with a usage error argument exits 2 in json", mode: "exit-usage-arg", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, ErrorCodeUsage, env.ErrorCode)
				assert.Equal(t, ExitCodeUsageError, env.ExitCode)
			},
		},
		{
			name: "coded error helper with a usage error argument exits 2", mode: "with-code-usage-arg", format: OutputFormatTable, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "Error: Failed: invalid note ID: invalid id.")
			},
		},
		{
			name: "coded error helper with a usage error argument exits 2 in json", mode: "with-code-usage-arg", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, ErrorCodeUsage, env.ErrorCode)
				assert.Equal(t, ExitCodeUsageError, env.ExitCode)
			},
		},
		{
			name: "exit code 1 with a usage error argument exits 2", mode: "exit-code-1-usage-arg", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Equal(t, ExitCodeUsageError, decodeSingleLineEnvelope(t, stderr).ExitCode)
			},
		},
		{
			name: "envelope helper at exit code 1 with a usage error exits 2", mode: "envelope-code-1-usage", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Equal(t, ErrorCodeUsage, decodeSingleLineEnvelope(t, stderr).ErrorCode)
			},
		},
		{
			name: "envelope helper keeps a chosen exit code other than 1", mode: "envelope-code-6-usage", format: OutputFormatTable, wantExit: ExitCodeNotApproved,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "Error: Failed: invalid note ID: invalid id.")
			},
		},
		{
			name: "a server answer coded usage_error stays exit 1 with its status", mode: "exit-server-usage-code", format: OutputFormatJSON, wantExit: ExitCodeGeneralError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, ExitCodeGeneralError, env.ExitCode)
				assert.Equal(t, ErrorCodeUsage, env.ErrorCode)
				assert.Equal(t, 400, env.HTTPStatus)
			},
		},
		{
			name: "a server answer coded usage_error stays exit 1 in table", mode: "exit-server-usage-code", format: OutputFormatTable, wantExit: ExitCodeGeneralError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "For issues")
			},
		},
		{
			name: "table usage helper exits 2 without the report footer", mode: "usage-no-operation", format: OutputFormatTable, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "bad flag --x")
				assert.NotContains(t, stderr, "report")
			},
		},
		{
			name: "json usage helper carries usage_error", mode: "usage-no-operation", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, ErrorCodeUsage, env.ErrorCode)
				assert.Equal(t, ExitCodeUsageError, env.ExitCode)
				assert.Equal(t, "bad flag --x", env.Message)
			},
		},
		{
			name: "usage helper", mode: "usage", format: OutputFormatJSON, wantExit: ExitCodeUsageError,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, ErrorCodeUsage, env.ErrorCode)
				assert.Equal(t, ExitCodeUsageError, env.ExitCode)
			},
		},
		{
			name: "failure json exits with its code", mode: "failure", format: OutputFormatJSON, wantExit: ExitCodeNotApproved,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				env := decodeSingleLineEnvelope(t, stderr)
				assert.Equal(t, ExitCodeNotApproved, env.ExitCode)
				assert.Equal(t, ErrorCodeNotApproved, env.ErrorCode)
			},
		},
		{
			name: "failure table returns silently", mode: "failure", format: OutputFormatTable, wantExit: helperReturned,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Empty(t, stderr)
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
	usageArg := MarkUsageError(fmt.Errorf("invalid note ID: %w", ErrInvalidID))
	switch mode {
	case "exit":
		CliErrorWithExit("Failed: %s.", "boom")
	case "exit-err":
		CliErrorWithExit("Failed %s: %s.", "list", &fakeAPIError{status: 404})
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
	case "exit-usage-arg":
		CliErrorWithExit("Failed: %s.", usageArg)
	case "with-code-usage-arg":
		CliErrorWithCode(ExitCodeGeneralError, "", "Failed: %s.", usageArg)
	case "exit-code-1-usage-arg":
		CliErrorWithExitCode(ExitCodeGeneralError, "Failed: %s.", usageArg)
	case "envelope-code-1-usage":
		CliErrorEnvelopeWithExitCode(ExitCodeGeneralError, "", usageArg, "Failed: %s.", usageArg)
	case "envelope-code-6-usage":
		CliErrorEnvelopeWithExitCode(ExitCodeNotApproved, "", usageArg, "Failed: %s.", usageArg)
	case "exit-server-usage-code":
		CliErrorWithExit("Failed: %s.", &fakeAPIError{code: ErrorCodeUsage, status: 400})
	case "usage-no-operation":
		CliUsageErrorWithExit("bad flag --%s", "x")
	case "prompt-eof":
		PromptForInput("Username: ")
	case "failure":
		ExitWithFailureJSON(Failure{ExitCode: ExitCodeNotApproved, ErrorCode: ErrorCodeNotApproved, Message: "no"})
	}
	os.Exit(helperReturned)
}

// helperReturned is the helper's exit code when the helper under test did not exit.
const helperReturned = 97

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

// A partial success keeps the attach refusal out of error_code but still shows its hint.
func TestCliErrorEnvelopeWithExit_NilErrorStillShowsTheHint(t *testing.T) {
	for _, format := range []string{OutputFormatJSON, OutputFormatTable} {
		t.Run(format, func(t *testing.T) {
			help := exec.Command(os.Args[0], "-test.run=^TestCliErrorEnvelopeWithExitNilErrorHelperProcess$")
			help.Env = append(os.Environ(), "GO_WANT_NIL_ERROR_ENVELOPE_HELPER="+format)
			var stderr bytes.Buffer
			help.Stderr = &stderr

			err := help.Run()

			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, 1, exitErr.ExitCode())
			const message = "Work session created (ws-1) but failed to set as active: permission denied. Run 'alpacon work-session use ws-1' to retry."
			if format == OutputFormatTable {
				assert.Contains(t, stderr.String(), message+"\nHint: "+sampleHint+"\n")
				return
			}
			var envelope JSONErrorEnvelope[cliErrorCtx]
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
			assert.Equal(t, ErrorCodeCLI, envelope.ErrorCode)
			assert.Equal(t, message, envelope.Message)
			assert.Equal(t, []NextAction{{Description: sampleHint}}, envelope.NextActions)
		})
	}
}

func TestCliErrorEnvelopeWithExitNilErrorHelperProcess(t *testing.T) {
	format := os.Getenv("GO_WANT_NIL_ERROR_ENVELOPE_HELPER")
	if format == "" {
		return
	}
	OutputFormat = format
	colorEnabled = false
	useErr := &hintedError{code: "permission_denied", hint: sampleHint}
	CliErrorEnvelopeWithExit("create", nil, "Work session created (%s) but failed to set as active: %s. Run 'alpacon work-session use %s' to retry.", "ws-1", useErr, "ws-1")
}
