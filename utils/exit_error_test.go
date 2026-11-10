package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithExitError_SilencesCobraOnlyForAnExitError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		err         error
		wantSilence bool
	}{
		{name: "exit error", err: NewSilentExitError(ExitCodeGeneralError), wantSilence: true},
		{name: "plain error", err: errors.New("unreported"), wantSilence: false},
		{name: "success", err: nil, wantSilence: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{}
			runE := WithExitError(func(*cobra.Command, []string) error { return tc.err })

			err := runE(cmd, nil)

			assert.Equal(t, tc.err, err)
			assert.Equal(t, tc.wantSilence, cmd.SilenceErrors)
			assert.Equal(t, tc.wantSilence, cmd.SilenceUsage)
		})
	}
}

func TestFprintExitError_PrintsTheFailureItsConstructorNames(t *testing.T) {
	t.Parallel()
	usageCause := MarkUsageError(errors.New("bad flag"))
	cases := []struct {
		name       string
		err        error
		wantCode   int
		wantLine   string
		wantFooter bool
	}{
		{name: "general error keeps the footer", err: NewExitError("lookup failed: %s", errors.New("boom")), wantCode: ExitCodeGeneralError, wantLine: "lookup failed: boom\n", wantFooter: true},
		{name: "chosen code drops the footer", err: NewExitErrorWithCode(ExitCodeServerBusy, "server busy"), wantCode: ExitCodeServerBusy, wantLine: "server busy\n"},
		{name: "usage error", err: NewUsageExitError("expected one %s", "ENDPOINT"), wantCode: ExitCodeUsageError, wantLine: "expected one ENDPOINT\n"},
		{name: "empty message still prints the error line", err: NewExitError(""), wantCode: ExitCodeGeneralError, wantLine: ": \n", wantFooter: true},
		{name: "usage cause promotes exit one to two", err: NewExitError("%s", usageCause), wantCode: ExitCodeUsageError, wantLine: "bad flag\n"},
		{name: "silent prints nothing", err: NewSilentExitError(ExitCodeGeneralError), wantCode: ExitCodeGeneralError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			exitErr, ok := errors.AsType[*ExitError](tc.err)
			require.True(t, ok, "want a *ExitError, got %T", tc.err)

			FprintExitError(&out, exitErr)

			assert.Equal(t, tc.wantCode, exitErr.Code)
			if tc.wantLine == "" {
				assert.Empty(t, out.String())
				return
			}
			assert.Contains(t, out.String(), tc.wantLine)
			if tc.wantFooter {
				assert.Contains(t, out.String(), gitIssueURL)
			} else {
				assert.NotContains(t, out.String(), gitIssueURL)
			}
		})
	}
}

func TestExitError_UnwrapsTheCauseAmongItsArguments(t *testing.T) {
	t.Parallel()
	cause := errors.New("boom")

	err := NewExitError("lookup failed: %s", cause)

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "lookup failed: boom")
}

type hintedAPIError struct {
	fakeAPIError
	hint string
}

func (e *hintedAPIError) ErrorHint() string { return e.hint }

// Serial: OutputFormat is process-wide.
func TestFprintExitError_UnderJSONPrintsOneEnvelope(t *testing.T) {
	saved := OutputFormat
	OutputFormat = OutputFormatJSON
	t.Cleanup(func() { OutputFormat = saved })
	refusal := &hintedAPIError{fakeAPIError: fakeAPIError{code: "rbac_permission_required", status: 403}, hint: sampleHint}
	cases := []struct {
		name           string
		err            error
		wantErrorCode  string
		wantHTTPStatus int
		wantHint       bool
	}{
		{name: "server refusal", err: NewExitError("request failed: %s", refusal), wantErrorCode: "rbac_permission_required", wantHTTPStatus: 403, wantHint: true},
		{name: "usage error", err: NewUsageExitError("expected one %s", "ENDPOINT"), wantErrorCode: ErrorCodeUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exitErr, ok := errors.AsType[*ExitError](tc.err)
			require.True(t, ok, "want a *ExitError, got %T", tc.err)
			var out bytes.Buffer

			FprintExitError(&out, exitErr)

			var env JSONErrorEnvelope[cliErrorCtx]
			require.NoError(t, json.Unmarshal(out.Bytes(), &env))
			assert.Equal(t, exitErr.Code, env.ExitCode)
			assert.Equal(t, tc.wantErrorCode, env.ErrorCode)
			assert.Equal(t, tc.wantHTTPStatus, env.HTTPStatus)
			if tc.wantHint {
				assert.Contains(t, env.NextActions, NextAction{Description: sampleHint})
			} else {
				assert.Empty(t, env.NextActions)
			}
			assert.NotContains(t, out.String(), gitIssueURL)
		})
	}
}

func TestFprintExitError_SilentPrintsNothingUnderJSON(t *testing.T) {
	saved := OutputFormat
	OutputFormat = OutputFormatJSON
	t.Cleanup(func() { OutputFormat = saved })
	exitErr, ok := errors.AsType[*ExitError](NewSilentExitError(ExitCodeGeneralError))
	require.True(t, ok)
	var out bytes.Buffer

	FprintExitError(&out, exitErr)

	assert.Empty(t, out.String())
}
