package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"

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

func TestBuildCliErrorEnvelope_NoCodeOmitsField(t *testing.T) {
	t.Parallel()
	env := buildCliErrorEnvelope("use", "", "boom")

	rendered, err := FormatJSON(env)
	require.NoError(t, err)
	assert.NotContains(t, rendered, "error_code")
	assert.Contains(t, rendered, `"operation": "use"`)
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

	assert.Empty(t, env.ErrorCode)
	assert.Equal(t, "No recordings found for session ses-1.", env.Message)
}

func TestBuildCliErrorEnvelopeFromErr_PlainErr(t *testing.T) {
	t.Parallel()
	env := buildCliErrorEnvelopeFromErr("use", errors.New("config write failed"), "config write failed")

	assert.Empty(t, env.ErrorCode)
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

// A partial success passes nil so the envelope carries no error_code, yet the
// refusal among the message's arguments still shows its hint, in table output
// and in the JSON envelope's next_actions.
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
			assert.Empty(t, envelope.ErrorCode, "a partial success carries no error_code")
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
