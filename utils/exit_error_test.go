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

func TestFprintExitError_PrintsWhatTheEnvelopeConstructorsName(t *testing.T) {
	t.Parallel()
	usageCause := MarkUsageError(errors.New("bad flag"))
	cases := []struct {
		name       string
		err        error
		wantCode   int
		wantLine   string
		wantFooter bool
	}{
		{name: "general error keeps the footer", err: NewEnvelopeExitError("kube_login", errors.New("boom"), "login failed: %s", "boom"), wantCode: ExitCodeGeneralError, wantLine: "login failed: boom\n", wantFooter: true},
		{name: "chosen code drops the footer", err: NewEnvelopeExitErrorWithCode(ExitCodeGeneralError, "kube_logout", nil, "in use"), wantCode: ExitCodeGeneralError, wantLine: "in use\n"},
		{name: "usage error", err: NewUsageEnvelopeExitError("kube_login", "--cluster must name a cluster."), wantCode: ExitCodeUsageError, wantLine: "--cluster must name a cluster.\n"},
		{name: "usage cause promotes exit one to two", err: NewEnvelopeExitError("kube_logout", usageCause, "%s", usageCause), wantCode: ExitCodeUsageError, wantLine: "bad flag\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			exitErr, _ := errors.AsType[*ExitError](tc.err)

			FprintExitError(&out, exitErr)

			assert.Equal(t, tc.wantCode, exitErr.Code)
			assert.Contains(t, out.String(), tc.wantLine)
			if tc.wantFooter {
				assert.Contains(t, out.String(), gitIssueURL)
			} else {
				assert.NotContains(t, out.String(), gitIssueURL)
			}
		})
	}
}

// Serial: it sets OutputFormat.
func TestFprintExitError_EnvelopeNamesTheOperationAndTheCause(t *testing.T) {
	old := OutputFormat
	OutputFormat = OutputFormatJSON
	t.Cleanup(func() { OutputFormat = old })
	cause := errors.New("boom")
	var out bytes.Buffer

	FprintExitError(&out, NewEnvelopeExitError("kube_login", cause, "login failed: %s", cause).(*ExitError))

	var envelope struct {
		ExitCode int    `json:"exit_code"`
		Message  string `json:"message"`
		Context  struct {
			Operation string `json:"operation"`
		} `json:"context"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &envelope), out.String())
	assert.Equal(t, ExitCodeGeneralError, envelope.ExitCode)
	assert.Equal(t, "login failed: boom", envelope.Message)
	assert.Equal(t, "kube_login", envelope.Context.Operation)
	assert.NotContains(t, out.String(), gitIssueURL)
}

func TestWorkSessionExitError_IsTheGateDiagnosticOrNothing(t *testing.T) {
	t.Parallel()
	refusal := errors.New(`{"code": "work_session_required"}`)

	t.Run("unrelated error", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, WorkSessionExitError(errors.New("boom"), OperationKube, "prod-east", "Browser login", ""))
	})
	t.Run("gate refusal", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		exitErr := WorkSessionExitError(refusal, OperationKube, "prod-east", "Browser login", "")

		FprintExitError(&out, exitErr)

		require.NotNil(t, exitErr)
		assert.Equal(t, ExitCodeWorkSessionDenied, exitErr.Code)
		require.ErrorIs(t, exitErr, refusal)
		assert.Equal(t, buildWorkSessionDiagnostic(WorkSessionRequired, OperationKube, "prod-east", "Browser login", "", "")+"\n", out.String())
	})
}
