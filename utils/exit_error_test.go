package utils

import (
	"bytes"
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
		{name: "exit error", err: &ExitError{Code: ExitCodeGeneralError}, wantSilence: true},
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
		{name: "usage cause promotes exit one to two", err: NewExitError("%s", usageCause), wantCode: ExitCodeUsageError, wantLine: "bad flag\n"},
		{name: "code alone prints nothing", err: &ExitError{Code: ExitCodeGeneralError}, wantCode: ExitCodeGeneralError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			exitErr, _ := errors.AsType[*ExitError](tc.err)

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

func TestWorkSessionExitError_PrintsTheGateDiagnosticAndExits3(t *testing.T) {
	t.Parallel()
	refusal := errors.New(`{"code": "work_session_required"}`)

	err := WorkSessionExitError(refusal, OperationWebsh, "my-server", "Browser login", "")

	require.ErrorIs(t, err, refusal)
	exitErr, ok := errors.AsType[*ExitError](err)
	require.True(t, ok, "want an ExitError, got %v", err)
	assert.Equal(t, ExitCodeWorkSessionDenied, exitErr.Code)
	var stderr bytes.Buffer
	FprintExitError(&stderr, exitErr)
	assert.Equal(t, buildWorkSessionDiagnostic(WorkSessionRequired, OperationWebsh, "my-server", "Browser login", "", "")+"\n", stderr.String())
}

func TestWorkSessionExitError_IsNilForAnyOtherError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
	}{
		{name: "nil", err: nil},
		{name: "other code", err: errors.New(`{"code": "auth_mfa_required"}`)},
		{name: "no code", err: errors.New("connection refused")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := WorkSessionExitError(tc.err, OperationWebsh, "my-server", "Browser login", "")

			assert.NoError(t, err)
		})
	}
}

func TestNewParseExitError_PointsToTheCommandsHelp(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "websh"}

	err := NewParseExitError(cmd, "", "unknown flag: --bogus")

	exitErr, ok := errors.AsType[*ExitError](err)
	require.True(t, ok, "want an ExitError, got %v", err)
	assert.Equal(t, ExitCodeUsageError, exitErr.Code)
	assert.Equal(t, "unknown flag: --bogus\nRun 'websh --help' for usage.", exitErr.Error())
}

func TestCheckPositiveInt_RefusesZeroAndBelowAsAUsageError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		value    int
		wantCode int
	}{
		{name: "positive", value: 1},
		{name: "zero", value: 0, wantCode: ExitCodeUsageError},
		{name: "negative", value: -3, wantCode: ExitCodeUsageError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CheckPositiveInt("tail", tc.value)

			if tc.wantCode == 0 {
				assert.NoError(t, err)
				return
			}
			exitErr, ok := errors.AsType[*ExitError](err)
			require.True(t, ok, "want an ExitError, got %v", err)
			assert.Equal(t, tc.wantCode, exitErr.Code)
			assert.Equal(t, "--tail must be a positive integer.", exitErr.Error())
		})
	}
}
