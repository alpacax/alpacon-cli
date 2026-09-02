package utils

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
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
	assert.NoError(t, err)
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
